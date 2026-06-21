// live.js — the shared SSE trigger-bus client (sty_b6e39eb8).
//
// One EventSource per page, opened against /events. The stream is a doorbell:
// each message carries only a topic string (e.g. "story:sty_x",
// "project:proj_y"), never row data. A page registers interest in the topics
// it shows via live.on(topic, fn); when a matching trigger arrives, fn runs and
// the page re-fetches its own data over a normal authorized request.
//
//   live.on("project:" + projectId, refreshList);
//   live.off("project:" + projectId, refreshList);
//
// Reconnect is the browser's: EventSource auto-reconnects with backoff after a
// drop (which happens on every deploy here), and the page's handler refetches
// on the next trigger, so the UI self-heals. No manual reconnect logic.
(function () {
  "use strict";
  if (window.live) return; // one client per page

  var handlers = Object.create(null); // topic -> [fn, ...]
  var source = null;
  var bounced = false;

  // bounceToLogin navigates the whole page to /login, carrying the current
  // view (path + query — the filter/expansion) as a same-site `next` so login
  // returns the user exactly here. Guarded so concurrent live calls bounce once.
  function bounceToLogin() {
    if (bounced) return;
    bounced = true;
    var here = window.location.pathname + window.location.search;
    window.location.assign("/login?next=" + encodeURIComponent(here));
  }
  window.satellites = window.satellites || {};
  window.satellites.bounceToLogin = bounceToLogin;

  function dispatch(topic) {
    var fns = handlers[topic];
    if (!fns) return;
    for (var i = 0; i < fns.length; i++) {
      try {
        fns[i](topic);
      } catch (e) {
        if (window.console) console.error("live handler for", topic, e);
      }
    }
  }

  function connect() {
    if (!window.EventSource) return; // unsupported browser — page stays static
    source = new EventSource("/events");
    source.addEventListener("trigger", function (ev) {
      dispatch(ev.data);
    });
    // A transient drop (deploy blip) leaves readyState === CONNECTING and the
    // browser auto-reconnects — no action. But an /events handshake that gets a
    // 401 (stale session) closes the stream permanently (readyState === CLOSED);
    // that is logout, so bounce the page to /login.
    source.onerror = function () {
      if (source && source.readyState === EventSource.CLOSED) {
        bounceToLogin();
      }
    };
  }

  window.live = {
    // Register fn to run when topic fires. Returns an unsubscribe function.
    on: function (topic, fn) {
      (handlers[topic] || (handlers[topic] = [])).push(fn);
      if (!source) connect();
      var self = this;
      return function () {
        self.off(topic, fn);
      };
    },
    // Remove a previously-registered handler.
    off: function (topic, fn) {
      var fns = handlers[topic];
      if (!fns) return;
      var i = fns.indexOf(fn);
      if (i !== -1) fns.splice(i, 1);
      if (fns.length === 0) delete handlers[topic];
    },
  };
})();
