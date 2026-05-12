// EPUB viewer entry point. Loaded from
// https://appassets.androidplatform.net/assets/epub-viewer/index.html
// by the BookReaderScreen WebView. The .epub bytes are pre-fetched
// on the Kotlin side and served back via a second path handler at
// /epub/file.epub on the same authority — so the same-origin fetch
// here just works (no auth header needed in JS).
//
// The Kotlin side calls into us via a small JS bridge:
//   AndroidBridge.onPageChange(pageNum)   — notify when the user flips
// and we expose:
//   window.goPage(n)     — Kotlin-driven slider/keys
//   window.goNext()/goPrev()
//
// Page-count semantics match the web client: for EPUB we report
// spine length (chapter count); page-flips inside a chapter just
// move the viewport, not the page number.

(function () {
  var err = document.getElementById('err');
  var viewer = document.getElementById('viewer');

  function showError(msg) {
    err.textContent = msg;
    err.classList.remove('hidden');
    viewer.classList.add('hidden');
    bridge('onError', msg);
  }

  function bridge(name, payload) {
    try {
      if (window.AndroidBridge && typeof window.AndroidBridge[name] === 'function') {
        window.AndroidBridge[name](payload == null ? '' : String(payload));
      }
    } catch (_) { /* swallow */ }
  }

  var params = new URLSearchParams(window.location.search);
  var epubPath = params.get('file') || '/epub/file.epub';
  var startPage = parseInt(params.get('p') || '1', 10) || 1;

  if (typeof ePub !== 'function') {
    showError('epub.js failed to load.');
    return;
  }

  console.log('viewer: fetch start ' + epubPath);
  fetch(epubPath)
    .then(function (resp) {
      console.log('viewer: fetch headers ' + resp.status);
      if (!resp.ok) throw new Error('Fetch failed: ' + resp.status);
      return resp.arrayBuffer();
    })
    .then(function (buf) {
      console.log('viewer: arrayBuffer ' + buf.byteLength + ' bytes — parsing');
      var book = ePub(buf);
      // flow: 'scrolled-doc' + manager: 'default' — one section
      // visible at a time, the section is a vertically scrollable
      // iframe, tap zones page between sections.
      //
      // Why not 'paginated' + 'default': column-pagination math
      // broke against the WebView's viewport, pages rendered into
      // off-screen columns (whole reader looked white). Why not
      // 'continuous': keeps every section alive at once, exhausts
      // memory on >100-section books and crashes the WebView
      // renderer with a Crashpad minidump.
      //
      // Pin width/height to numeric pixels — passing '100%' lets
      // epub.js layout against an unresolved parent size and the
      // first iframe paints at 0×0.
      var w = viewer.clientWidth || window.innerWidth || 360;
      var h = viewer.clientHeight || window.innerHeight || 640;
      console.log('viewer: container size ' + w + 'x' + h);
      var rendition = book.renderTo(viewer, {
        width: w,
        height: h,
        flow: 'scrolled-doc',
        manager: 'default',
        // Inline resources are served as blob:; the rendition iframe
        // sandbox needs allow-scripts + allow-same-origin for them
        // to load, otherwise images and stylesheets inside the EPUB
        // resolve to broken placeholders.
        allowScriptedContent: true,
      });

      // Belt + suspenders: an EPUB stylesheet that omits a body
      // background lets the WebView's background show through. Pin
      // a white-on-black theme so the chapter is readable regardless
      // of the source CSS.
      rendition.themes.register('onscreen', {
        body: { background: '#ffffff', color: '#111111' },
        'html, body': { background: '#ffffff', color: '#111111' },
      });
      rendition.themes.select('onscreen');

      // Tap-to-paginate. epub.js paginated mode has no built-in
      // touch handler — the chapter iframe just sits there. We
      // inject click + touch listeners into each rendered iframe via
      // rendition.hooks.content; left 30% goes prev, right 30% goes
      // next, the middle is left alone so future chrome (TOC,
      // settings) can grab it. Same pattern Jellyfin's reader uses.
      // Tap dedupe: a single touch on mobile fires touchend AND a
      // synthetic click ~3 ms later. Without this, every tap pages
      // twice. We grab the touchend, mark it handled, then ignore
      // any click within 500 ms.
      var lastTapAt = 0;
      function tapAction(x, w, label) {
        var now = Date.now();
        if (now - lastTapAt < 500) {
          console.log('viewer: tap ' + label + ' x=' + x + ' (deduped)');
          return;
        }
        lastTapAt = now;
        console.log('viewer: tap ' + label + ' x=' + x + ' w=' + w);
        if (x < w * 0.3) {
          console.log('viewer: rendition.prev()');
          rendition.prev();
        } else if (x > w * 0.7) {
          console.log('viewer: rendition.next()');
          rendition.next();
        } else {
          console.log('viewer: middle tap — ignored');
        }
      }
      function attachGestures(doc, label) {
        doc.addEventListener('click', function (e) {
          tapAction(
            e.clientX,
            doc.documentElement.clientWidth || window.innerWidth,
            label + ' click',
          );
        }, true);
        // touchend gives us coordinates and fires before the synthetic
        // click; we handle it, then dedupe the click that follows.
        var touchStartX = 0;
        doc.addEventListener('touchstart', function (e) {
          touchStartX = e.touches[0].clientX;
        }, { passive: true });
        doc.addEventListener('touchend', function (e) {
          var endX = (e.changedTouches[0] || {}).clientX || touchStartX;
          var dx = endX - touchStartX;
          if (Math.abs(dx) > 60) {
            // Big swipe wins over a misread tap.
            console.log('viewer: swipe dx=' + dx);
            lastTapAt = Date.now();
            if (dx < 0) rendition.next();
            else rendition.prev();
            return;
          }
          tapAction(
            endX,
            doc.documentElement.clientWidth || window.innerWidth,
            label + ' touch',
          );
        }, { passive: true });
      }
      rendition.hooks.content.register(function (contents) {
        console.log('viewer: content hook fired — attaching gestures to iframe');
        try { attachGestures(contents.document, 'iframe'); }
        catch (e) { console.log('viewer: iframe gesture attach failed ' + (e && e.message)); }
      });
      // Also catch taps that miss the iframe entirely (margins around
      // the rendition viewport) by listening on the host document.
      attachGestures(document, 'host');

      rendition.on('relocated', function (loc) {
        try {
          var idx = (loc && loc.start && typeof loc.start.index === 'number') ? loc.start.index : 0;
          var href = (loc && loc.start && loc.start.href) || '';
          var spineLen = (book.spine && book.spine.length) ? book.spine.length : 1;
          console.log('viewer: relocated idx=' + (idx + 1) + '/' + spineLen + ' href=' + href);
          bridge('onPageChange', (idx + 1) + ':' + spineLen);
        } catch (e) {
          console.log('viewer: relocated error ' + (e && e.message));
        }
      });

      rendition.on('linkClicked', function (href) {
        if (/^https?:\/\//i.test(href)) {
          bridge('onExternalLink', href);
        }
      });

      window.goPage = function (n) {
        if (!book.spine || !book.spine.get) return;
        var target = book.spine.get(Math.max(0, n - 1));
        if (target) rendition.display(target.href);
      };
      window.goNext = function () { rendition.next(); };
      window.goPrev = function () { rendition.prev(); };

      // book.ready resolves once the OPF + spine are parsed.
      // Calling rendition.display() before that throws "No Section
      // Found" because rendition.display(undefined) tries to land on
      // spine.first() — which is empty until the manifest is read.
      //
      // We deliberately do NOT call book.locations.generate(): on
      // multi-tens-of-MB books it walks the entire spine in a single
      // synchronous-ish traversal, freezes the WebView for 30+
      // seconds, and gives us nothing today (we don't expose a
      // percentage scrubber). Spine-index progress is enough.
      book.ready
        .then(function () {
          console.log('viewer: book.ready — spine length ' + (book.spine && book.spine.length));
          var initial = book.spine && book.spine.get
            ? book.spine.get(Math.max(0, startPage - 1))
            : null;
          console.log('viewer: rendition.display ' + (initial && initial.href));
          return rendition.display(initial && initial.href);
        })
        .then(function () {
          console.log('viewer: rendition.display resolved');
          bridge('onReady', '');
        })
        .catch(function (e) {
          console.log('viewer: error ' + (e && e.message));
          showError((e && e.message) ? e.message : 'Failed to display book.');
        });
    })
    .catch(function (e) {
      showError(e && e.message ? e.message : 'Failed to load book.');
    });
})();
