package portalreplicate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// DefaultActionTimeout is the per-action wall-clock cap when the
// caller did not set Action.TimeoutMs. Aligned with the chromedp
// default for WaitVisible.
const DefaultActionTimeout = 5 * time.Second

// MaxActions caps the per-call action count so a runaway sequence
// can't pin a chromium tab indefinitely.
const MaxActions = 50

// RunOptions bundles the call-site inputs to Run that aren't part of
// the Action sequence itself.
type RunOptions struct {
	// TargetURL is the absolute base URL the runner navigates to
	// before any actions execute. ActionNavigate's Value can override
	// per-step.
	TargetURL string

	// Cookies are injected before the first navigate. Domain/Path
	// default to the TargetURL host + "/" when empty.
	Cookies []Cookie

	// Headless toggles chromium headless mode. Default true. Tests
	// that need to observe the browser visually flip this off.
	Headless bool

	// AllocatorOpts can override / extend the default chromedp
	// ExecAllocator opts. nil = use defaults.
	AllocatorOpts []chromedp.ExecAllocatorOption
}

// Run executes actions in order against TargetURL. Returns one
// Result per action plus a Summary rolling them up. The first failed
// action stops execution; subsequent actions get StatusSkipped
// Results so the caller sees the whole shape.
//
// The caller is responsible for ensuring chromium is reachable on
// the runtime. When chromedp can't allocate a browser, Run returns
// an error before any Result is produced.
func Run(ctx context.Context, opts RunOptions, actions []Action) ([]Result, Summary, error) {
	startedAt := time.Now()
	if len(actions) == 0 {
		return nil, Summary{TargetURL: opts.TargetURL, Status: StatusOK, StartedAt: startedAt, FinishedAt: startedAt}, nil
	}
	if len(actions) > MaxActions {
		return nil, Summary{}, fmt.Errorf("portalreplicate: %d actions exceeds limit %d", len(actions), MaxActions)
	}
	if opts.TargetURL == "" {
		return nil, Summary{}, errors.New("portalreplicate: TargetURL is required")
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", opts.Headless || true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if len(opts.AllocatorOpts) > 0 {
		allocOpts = append(allocOpts, opts.AllocatorOpts...)
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	// Console capture: chromedp surfaces runtime.consoleAPICalled
	// through chromedp.ListenTarget. We accumulate into a slice
	// guarded by a mutex; ActionConsoleCapture reads + clears.
	console := newConsoleSink(browserCtx)

	// Inject cookies before navigate. We do this once up front so the
	// initial navigate honours auth.
	if err := injectCookies(browserCtx, opts.TargetURL, opts.Cookies); err != nil {
		return nil, Summary{}, fmt.Errorf("portalreplicate: inject cookies: %w", err)
	}

	results := make([]Result, len(actions))
	summary := Summary{TargetURL: opts.TargetURL, StartedAt: startedAt}
	stopAt := -1
	for i, a := range actions {
		if stopAt >= 0 {
			results[i] = Result{Action: a, Status: StatusSkipped, StartedAt: time.Now()}
			summary.Skipped++
			continue
		}
		start := time.Now()
		result := runAction(browserCtx, opts, a, console)
		result.Duration = time.Since(start)
		result.StartedAt = start
		results[i] = result
		if result.Status == StatusFailed {
			summary.Failed++
			stopAt = i
		} else {
			summary.Passed++
		}
	}
	summary.Total = len(actions)
	summary.FinishedAt = time.Now()
	summary.Duration = summary.FinishedAt.Sub(summary.StartedAt)
	if summary.Failed > 0 {
		summary.Status = StatusFailed
	} else {
		summary.Status = StatusOK
	}
	return results, summary, nil
}

// runAction dispatches one Action to the matching chromedp recipe.
// Returns a Result with Status set; the caller fills Duration +
// StartedAt. The action runs against the shared browserCtx — chromedp
// stores its frame/target state on that context and wrapping it in a
// stdlib timer context produces spurious "context canceled" failures
// between actions. Per-action timeouts are bounded by the parent
// context the caller passed to Run (typically a test or MCP-call
// timeout) rather than re-wrapping here.
func runAction(ctx context.Context, opts RunOptions, a Action, console *consoleSink) Result {
	r := Result{Action: a}
	switch a.Type {
	case ActionNavigate:
		target := a.Value
		if target == "" {
			target = opts.TargetURL
		}
		if err := chromedp.Run(ctx, chromedp.Navigate(target)); err != nil {
			return failed(r, fmt.Errorf("navigate %q: %w", target, err))
		}
	case ActionWaitVisible:
		if a.Selector == "" {
			return failed(r, errors.New("wait_visible requires a selector"))
		}
		bound := DefaultActionTimeout
		if a.TimeoutMs > 0 {
			bound = time.Duration(a.TimeoutMs) * time.Millisecond
		}
		// WithTimeout is safe specifically for wait_visible because
		// chromedp.WaitVisible respects ctx for its polling loop and
		// holds no async state after return — unlike chromedp.Navigate
		// which spawns load-event listeners that get poisoned when an
		// outer cancel fires.
		waitCtx, cancel := context.WithTimeout(ctx, bound)
		defer cancel()
		if err := chromedp.Run(waitCtx, chromedp.WaitVisible(a.Selector, chromedp.ByQuery)); err != nil {
			return failed(r, fmt.Errorf("wait_visible %q: %w", a.Selector, err))
		}
	case ActionClick:
		if a.Selector == "" {
			return failed(r, errors.New("click requires a selector"))
		}
		if err := chromedp.Run(ctx, chromedp.Click(a.Selector, chromedp.ByQuery)); err != nil {
			return failed(r, fmt.Errorf("click %q: %w", a.Selector, err))
		}
	case ActionDOMSnapshot:
		var html string
		sel := a.Selector
		if sel == "" {
			sel = "html"
		}
		if err := chromedp.Run(ctx, chromedp.OuterHTML(sel, &html, chromedp.ByQuery)); err != nil {
			return failed(r, fmt.Errorf("dom_snapshot %q: %w", a.Selector, err))
		}
		r.DOM = html
	case ActionConsoleCapture:
		r.ConsoleLogs = console.drain()
	case ActionScreenshot:
		var buf []byte
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90)); err != nil {
			return failed(r, fmt.Errorf("screenshot: %w", err))
		}
		r.Screenshot = buf
	case ActionDOMVisible:
		if a.Selector == "" {
			return failed(r, errors.New("dom_visible requires a selector"))
		}
		// Short bound to keep failed assertions snappy without
		// poisoning the shared browserCtx — context.WithTimeout is
		// safe here because we only invoke it for a single action's
		// lifetime, not across the run.
		bound := 500 * time.Millisecond
		if a.TimeoutMs > 0 {
			bound = time.Duration(a.TimeoutMs) * time.Millisecond
		}
		short, cancel := context.WithTimeout(ctx, bound)
		defer cancel()
		if err := chromedp.Run(short, chromedp.WaitVisible(a.Selector, chromedp.ByQuery)); err != nil {
			return failed(r, fmt.Errorf("dom_visible %q: %w", a.Selector, err))
		}
	default:
		return failed(r, fmt.Errorf("unknown action type %q", a.Type))
	}
	r.Status = StatusOK
	return r
}

func failed(r Result, err error) Result {
	r.Status = StatusFailed
	r.Error = err.Error()
	return r
}

// injectCookies sets each cookie on the browser before any
// navigation. Domain defaults to the host of TargetURL; Path
// defaults to "/".
func injectCookies(ctx context.Context, targetURL string, cookies []Cookie) error {
	if len(cookies) == 0 {
		return nil
	}
	u, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("parse target url: %w", err)
	}
	defaultDomain := u.Hostname()
	defaultPath := "/"
	expires := cdp.TimeSinceEpoch(time.Now().Add(24 * time.Hour))
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		for _, c := range cookies {
			if strings.TrimSpace(c.Name) == "" {
				return errors.New("cookie name is required")
			}
			domain := c.Domain
			if domain == "" {
				domain = defaultDomain
			}
			path := c.Path
			if path == "" {
				path = defaultPath
			}
			err := network.SetCookie(c.Name, c.Value).
				WithDomain(domain).
				WithPath(path).
				WithSecure(c.Secure).
				WithHTTPOnly(c.HTTPOnly).
				WithExpires(&expires).
				Do(ctx)
			if err != nil {
				return fmt.Errorf("set cookie %q: %w", c.Name, err)
			}
		}
		return nil
	}))
}

// consoleSink accumulates console messages from the browser via the
// chromedp event listener. drain() returns + clears the buffer.
type consoleSink struct {
	mu   sync.Mutex
	msgs []string
}

func newConsoleSink(ctx context.Context) *consoleSink {
	cs := &consoleSink{}
	chromedp.ListenTarget(ctx, func(ev any) {
		if call, ok := ev.(*runtime.EventConsoleAPICalled); ok {
			cs.mu.Lock()
			defer cs.mu.Unlock()
			parts := make([]string, 0, len(call.Args))
			for _, a := range call.Args {
				parts = append(parts, string(a.Value))
			}
			cs.msgs = append(cs.msgs, fmt.Sprintf("[%s] %s", call.Type, strings.Join(parts, " ")))
		}
	})
	return cs
}

func (c *consoleSink) drain() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.msgs...)
	c.msgs = c.msgs[:0]
	return out
}
