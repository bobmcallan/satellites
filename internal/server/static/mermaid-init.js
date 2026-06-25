// mermaid-init — generic, lazy mermaid rendering for document bodies (sty_89bd084f,
// epic:phases-task-outputs). goldmark renders a ```mermaid fence to
// <pre><code class="language-mermaid">…</code></pre>; this turns those blocks into
// rendered diagrams. The 3.3 MB mermaid bundle is fetched ONLY when a page actually
// contains a mermaid block, so pages with no diagram pay nothing. Not codegraph-
// specific — any document with a mermaid fence renders.
(function () {
  var loading = false, loaded = false, queue = [];

  function loadMermaid(cb) {
    if (loaded) { cb(window.mermaid); return; }
    queue.push(cb);
    if (loading) { return; }
    loading = true;
    var s = document.createElement('script');
    s.src = '/static/mermaid.min.js';
    s.onload = function () {
      loaded = true;
      queue.forEach(function (f) { f(window.mermaid); });
      queue = [];
    };
    s.onerror = function () { loading = false; }; // leave the source block visible
    document.head.appendChild(s);
  }

  // render converts the unrendered ```mermaid code blocks within root (default
  // the whole document) into diagrams. It is idempotent and re-runnable: blocks
  // already turned into .mermaid divs no longer match the selector, so a second
  // call only renders newly-injected blocks, and mermaid.run is scoped to those
  // new nodes. This is what lets lazily-loaded story-panel fragments (sty_d206e263)
  // render their diagrams — the page-load pass alone never sees them.
  function render(root) {
    root = root || document;
    var blocks = root.querySelectorAll('pre > code.language-mermaid');
    if (!blocks.length) { return; } // no diagram here — never load the bundle
    loadMermaid(function (mermaid) {
      if (!mermaid) { return; }
      var divs = [];
      blocks.forEach(function (code) {
        var pre = code.parentElement;
        var div = document.createElement('div');
        div.className = 'mermaid';
        div.textContent = code.textContent;
        pre.replaceWith(div);
        divs.push(div);
      });
      try {
        // strict securityLevel: diagram source can never inject script/HTML.
        mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'dark' });
        mermaid.run({ nodes: divs }); // only the blocks injected this pass
      } catch (e) { /* on failure the .mermaid text (the source) stays visible */ }
    });
  }

  // Re-render hook for client-injected DOM (story panel fragment swaps, live
  // refresh). Pass the swapped-in container so only its new blocks render.
  window.renderMermaid = render;

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { render(); });
  } else {
    render();
  }
})();
