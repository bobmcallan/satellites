// Server-fetch resolution for reviewer gates (sty_b8de4776). A gate dispatch
// resolves its skill body embed → local materialised → server; this file owns
// the SERVER step. The verb-layer dispatcher (internal/verb) holds only the
// GateBodyFetcher seam — it has no server/config access — so the CLI, which
// already talks to the substrate through verbs, supplies the implementation and
// injects it when it constructs the dispatcher.
//
// A substrate reviewer therefore needs no local install: absent from
// .claude/skills, its body is fetched by name and injected into `claude -p`
// just like an embedded gate. The fetch reuses the SAME scope-precedence
// resolution `skill sync` materialises by (mergeByPrecedence: project >
// workspace > system > global library) so a server hit is byte-identical to the
// body the local cache would have held — the cache stays a true offline cache,
// not a divergent source.

package cli

import (
	"context"
	"encoding/json"
	"io"

	"github.com/bobmcallan/satellites/internal/verb"
)

// serverGateFetcher returns the prod GateBodyFetcher the CLI wires into the gate
// dispatcher. It resolves a gate skill by name across every reachable scope and
// returns its raw SKILL.md (frontmatter + body). ok=false means no scope holds
// the named skill — not an error; the dispatcher then fails closed. A transport
// error (the substrate unreachable) is returned as err so a fetch failure is
// never silently read as a cache miss.
func serverGateFetcher(configArg, userArg string) verb.GateBodyFetcher {
	return func(ctx context.Context, skillName string) ([]byte, bool, error) {
		dispatch := func(ctx context.Context, name string, req json.RawMessage) (json.RawMessage, error) {
			return dispatchVerb(ctx, name, req, configArg, userArg)
		}
		// Resolve the workspace/project binding the way deploy/sync do — a
		// best-effort read of .satellites/satellites.toml. An unconfigured repo
		// tolerates the miss and resolves over system + global library only.
		ws, pj, _ := resolveDeployScope(ctx, configArg, userArg)
		return resolveServerGateBody(ctx, dispatch, configArg, ws, pj, skillName)
	}
}

// resolveServerGateBody is the server-resolution core, split out from the
// transport closure so it is unit-testable with a fake dispatch. It lists the
// substrate skills every reachable scope owns and returns the body of the one
// whose materialised name matches skillName, honouring the SAME precedence
// `skill sync` materialises by (mergeByPrecedence, last set wins: project >
// workspace > system > global library). ok=false when no scope holds the name.
func resolveServerGateBody(ctx context.Context, dispatch verbDispatch, configArg, ws, pj, skillName string) ([]byte, bool, error) {
	sysSet, err := listSubstrateSkills(ctx, dispatch, "system", "", "", false)
	if err != nil {
		return nil, false, err
	}
	var wsSet, pjSet []substrateSkill
	if ws != "" {
		if wsSet, err = listSubstrateSkills(ctx, dispatch, "workspace", ws, "", false); err != nil {
			return nil, false, err
		}
	}
	if pj != "" {
		if pjSet, err = listSubstrateSkills(ctx, dispatch, "project", ws, pj, false); err != nil {
			return nil, false, err
		}
	}
	libSet, err := listGlobalSkills(ctx, io.Discard, dispatch, configArg)
	if err != nil {
		return nil, false, err
	}

	// Match on the materialised name so a row whose source name omits the
	// `satellites-` prefix still resolves under the name the dispatch asked for.
	union := mergeByPrecedence(libSet, sysSet, wsSet, pjSet)
	for _, s := range union {
		if localSkillName(s.Name) == skillName {
			return []byte(s.Body), true, nil
		}
	}
	return nil, false, nil
}
