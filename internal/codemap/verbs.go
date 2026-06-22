package codemap

import (
	"github.com/bobmcallan/satellites/internal/mcpserver"
	"github.com/bobmcallan/satellites/internal/verb"
)

// CollectVerbEntries enumerates the MCP verb registry (internal/verb) and the
// advertised set (mcpserver.ExposedVerbs): every registered verb is an entry
// point, marked Exposed when the transport advertises it as an MCP tool. The
// handler symbol resolves to file:line via the index resolver. Lives in codemap
// (not the cli transport) so the cli package stays clear of the peer-transport
// import the layering guard forbids.
func CollectVerbEntries(resolve SymbolResolver) []EntryPoint {
	exposed := map[string]bool{}
	for _, n := range mcpserver.ExposedVerbs() {
		exposed[n] = true
	}
	var out []EntryPoint
	for _, name := range verb.Catalog() {
		v := verb.Get(name)
		if v == nil {
			continue
		}
		short := FuncShortName(v.Invoke)
		ep := EntryPoint{Surface: SurfaceMCP, Name: name, Handler: short, Exposed: exposed[name]}
		if resolve != nil {
			if f, l, ok := resolve(short); ok {
				ep.File, ep.Line = f, l
			}
		}
		out = append(out, ep)
	}
	return out
}
