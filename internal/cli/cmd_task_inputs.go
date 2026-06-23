// `satellites task inputs` — resolve a task's declared DOCUMENT INPUTS
// (epic:phases-task-outputs, B1). A task body MAY carry an optional `## Inputs`
// section with a fenced yaml block declaring the project documents the run should
// assess:
//
//	## Inputs
//
//	```yaml
//	tags: [phase:discovery]   # KV filter — project-scoped, AND containment
//	ids:  [doc_abc, doc_def]  # explicit ids (project-checked)
//	```
//
// The DECISION of which inputs lives in the task body (substrate); this command is
// thin MECHANISM that parses the declaration and enumerates via the existing
// document verbs, with the project ALWAYS pinned to the task's own project so the
// resolved set can never leak another project's documents (B1 AC3). The executor
// runs this during `running` to enumerate its inputs, then reads each body via
// `document_get` (or pass --read to fetch the bodies in one shot).

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bobmcallan/satellites/internal/verb"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// taskInputSpec is the parsed `## Inputs` declaration. Both fields are optional
// individually but a present block must declare at least one of them. `tags` is a
// KV filter (phase:/type: …, AND containment); `ids` names explicit documents.
type taskInputSpec struct {
	Tags []string `yaml:"tags"`
	IDs  []string `yaml:"ids"`
}

// parseTaskInputs extracts the `## Inputs` declaration from a task body.
//   - (spec, true, nil)  — a well-formed `## Inputs` block is present.
//   - (zero, false, nil) — no `## Inputs` section exists (inputs are optional).
//   - (zero, false, err) — the section is present but its yaml is malformed or it
//     declares neither tags nor ids.
func parseTaskInputs(body string) (taskInputSpec, bool, error) {
	yamlBlock, found := inputsYAMLBlock(body)
	if !found {
		return taskInputSpec{}, false, nil
	}
	var spec taskInputSpec
	if err := yaml.Unmarshal([]byte(yamlBlock), &spec); err != nil {
		return taskInputSpec{}, false, fmt.Errorf("## Inputs: malformed yaml: %w", err)
	}
	spec.Tags = trimNonEmpty(spec.Tags)
	spec.IDs = trimNonEmpty(spec.IDs)
	if len(spec.Tags) == 0 && len(spec.IDs) == 0 {
		return taskInputSpec{}, false, fmt.Errorf("## Inputs: declares neither tags nor ids")
	}
	return spec, true, nil
}

// inputsYAMLBlock returns the contents of the first fenced code block under an
// `## Inputs` heading (the section runs until the next `## ` heading or EOF). The
// fence may be ```yaml or a bare ```. Returns ("", false) when there is no
// `## Inputs` section, or one with no fenced block.
func inputsYAMLBlock(body string) (string, bool) {
	lines := strings.Split(body, "\n")
	inSection := false
	inFence := false
	var collected []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inSection {
			if isHeading(trimmed) && strings.EqualFold(headingText(trimmed), "Inputs") {
				inSection = true
			}
			continue
		}
		// In the Inputs section: a new `## ` heading ends it.
		if !inFence && isHeading(trimmed) {
			break
		}
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				continue
			}
			// Closing fence — first block wins.
			return strings.Join(collected, "\n"), true
		}
		if inFence {
			collected = append(collected, line)
		}
	}
	return "", false
}

// isHeading reports whether a trimmed line is a level-2 markdown heading (`## …`).
func isHeading(trimmed string) bool { return strings.HasPrefix(trimmed, "## ") }

// headingText returns the text of a `## …` heading, trimmed.
func headingText(trimmed string) string {
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "##"))
}

func trimNonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// resolvedInput is one document the task declares as an input.
type resolvedInput struct {
	ID   string
	Name string
	Type string
	Tags []string
}

// inputCandidate is a document fetched while resolving a spec, carrying the
// project it belongs to so the pure selection step can enforce the project pin.
type inputCandidate struct {
	input     resolvedInput
	projectID string
}

// selectInputs is the project-pin + dedup DECISION, kept pure so it is unit
// testable without a verb round-trip. It drops every candidate whose project is
// not the task's own project (so neither the tag query nor an explicit id can
// leak a foreign project's document — B1 AC3) and dedupes by id, preserving the
// order candidates were discovered.
func selectInputs(pinnedProject string, cands []inputCandidate) []resolvedInput {
	var out []resolvedInput
	seen := map[string]bool{}
	for _, c := range cands {
		if c.projectID != pinnedProject {
			continue
		}
		if c.input.ID == "" || seen[c.input.ID] {
			continue
		}
		seen[c.input.ID] = true
		out = append(out, c.input)
	}
	return out
}

func newTaskInputsCmd(configArg, userArg *string) *cobra.Command {
	var read bool
	cmd := &cobra.Command{
		Use:   "inputs <task-id>",
		Short: "Resolve a task's declared `## Inputs` documents (project-pinned; --read fetches bodies)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runTaskInputs(ctx, cmd.OutOrStdout(), *configArg, *userArg, strings.TrimSpace(args[0]), read)
		},
	}
	cmd.Flags().BoolVar(&read, "read", false, "Also fetch and print each resolved document's body (proves the inputs are readable)")
	return cmd
}

func runTaskInputs(ctx context.Context, out io.Writer, configPath, userArg, taskID string, read bool) error {
	// Resolve the task: confirm it IS a task and capture its project — the pin
	// every resolution below is scoped to.
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: taskID})
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
	if err != nil {
		return fmt.Errorf("task inputs: resolve %s: %w", taskID, err)
	}
	var taskResp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &taskResp); err != nil {
		return fmt.Errorf("task inputs: decode %s: %w", taskID, err)
	}
	if taskResp.Document.Type != taskType {
		return fmt.Errorf("task inputs: %s is type=%q, not a task", taskID, taskResp.Document.Type)
	}
	projectID := taskResp.Document.ProjectID
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("task inputs: task %s has no project_id", taskID)
	}
	body := taskResp.RawBody
	if body == "" && len(taskResp.Versions) > 0 {
		body = taskResp.Versions[len(taskResp.Versions)-1].Body
	}

	spec, ok, err := parseTaskInputs(body)
	if err != nil {
		return fmt.Errorf("task inputs: %w", err)
	}
	if !ok {
		fmt.Fprintln(out, "(no ## Inputs declared)")
		return nil
	}

	resolved, err := resolveTaskInputs(ctx, configPath, userArg, projectID, spec)
	if err != nil {
		return err
	}
	if len(resolved) == 0 {
		fmt.Fprintln(out, "(## Inputs declared, but no project documents matched)")
		return nil
	}

	for _, r := range resolved {
		tags := ""
		if len(r.Tags) > 0 {
			tags = "  [" + strings.Join(r.Tags, ", ") + "]"
		}
		fmt.Fprintf(out, "%s  %-10s  %s%s\n", r.ID, r.Type, r.Name, tags)
		if read {
			b, rErr := fetchBody(ctx, configPath, userArg, r.ID)
			if rErr != nil {
				return fmt.Errorf("task inputs: read %s: %w", r.ID, rErr)
			}
			fmt.Fprintf(out, "---\n%s\n---\n", strings.TrimRight(b, "\n"))
		}
	}
	return nil
}

// resolveTaskInputs turns a parsed spec into the deduped document set, with the
// project ALWAYS pinned to projectID. The tag filter lists project-scoped
// documents (row type=document); explicit ids are fetched and DROPPED when they
// belong to another project — so neither path can return a foreign project's docs.
func resolveTaskInputs(ctx context.Context, configPath, userArg, projectID string, spec taskInputSpec) ([]resolvedInput, error) {
	var cands []inputCandidate

	if len(spec.Tags) > 0 {
		listReq, _ := json.Marshal(verb.DocumentListRequest{
			Type:      "document",
			ProjectID: projectID,
			Tags:      spec.Tags,
			Limit:     200,
		})
		raw, err := dispatchVerb(ctx, "document_list", listReq, configPath, userArg)
		if err != nil {
			return nil, fmt.Errorf("task inputs: list by tags: %w", err)
		}
		var resp verb.DocumentListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("task inputs: decode list: %w", err)
		}
		for _, d := range resp.Items {
			cands = append(cands, inputCandidate{
				input:     resolvedInput{ID: d.ID, Name: d.Name, Type: d.Type, Tags: d.Tags},
				projectID: d.ProjectID,
			})
		}
	}

	for _, id := range spec.IDs {
		getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: id})
		raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
		if err != nil {
			return nil, fmt.Errorf("task inputs: resolve declared id %s: %w", id, err)
		}
		var resp verb.DocumentGetResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("task inputs: decode id %s: %w", id, err)
		}
		cands = append(cands, inputCandidate{
			input: resolvedInput{
				ID:   resp.Document.ID,
				Name: resp.Document.Name,
				Type: resp.Document.Type,
				Tags: resp.Document.Tags,
			},
			projectID: resp.Document.ProjectID,
		})
	}

	// Project-pin + dedup happen in one pure step (selectInputs): a tag-listed or
	// explicitly-named document from another project is dropped here.
	return selectInputs(projectID, cands), nil
}

func fetchBody(ctx context.Context, configPath, userArg, id string) (string, error) {
	getReq, _ := json.Marshal(verb.DocumentGetRequest{ID: id})
	raw, err := dispatchVerb(ctx, "document_get", getReq, configPath, userArg)
	if err != nil {
		return "", err
	}
	var resp verb.DocumentGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if resp.RawBody != "" {
		return resp.RawBody, nil
	}
	if len(resp.Versions) > 0 {
		return resp.Versions[len(resp.Versions)-1].Body, nil
	}
	return "", nil
}
