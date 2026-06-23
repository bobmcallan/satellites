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

  function render() {
    var blocks = document.querySelectorAll('pre > code.language-mermaid');
    if (!blocks.length) { return; } // no diagram on this page — never load the bundle
    loadMermaid(function (mermaid) {
      if (!mermaid) { return; }
      blocks.forEach(function (code) {
        var pre = code.parentElement;
        var div = document.createElement('div');
        div.className = 'mermaid';
        div.textContent = code.textContent;
        pre.replaceWith(div);
      });
      try {
        // strict securityLevel: diagram source can never inject script/HTML.
        mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'dark' });
        mermaid.run({ querySelector: '.mermaid' });
      } catch (e) { /* on failure the .mermaid text (the source) stays visible */ }
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', render);
  } else {
    render();
  }
})();
