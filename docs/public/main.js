(function () {
  document.querySelectorAll('pre[tabindex]').forEach(function (el) {
    el.setAttribute('tabindex', '-1');
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && document.activeElement && document.activeElement !== document.body) {
      document.activeElement.blur();
    }
  });
  (function () {
    var titleLink = document.querySelector('.title');
    if (!titleLink) return;
    var path = window.location.pathname.replace(/\/+$/, '') || '/';
    var href = titleLink.getAttribute('href').replace(/\/+$/, '') || '/';
    if (href === path) {
      titleLink.addEventListener('click', function (e) {
        e.preventDefault();
        window.scrollTo(0, 0);
      });
    }
  })();

  (function () {
    const header = document.querySelector('.header');
    if (!header) return;
    window.addEventListener('scroll', function () {
      header.classList.toggle('header_scrolled', window.scrollY > 30);
    }, { passive: true });
  })();

  (function () {
    const indexContent = document.querySelector('.index_content');
    const footerContent = document.querySelector('.footer_content');
    const header = document.querySelector('.header');
    if (!indexContent && !footerContent) return;
    var BORDER_BREAKPOINT = 700;
    function isNarrow() {
      return window.innerWidth <= BORDER_BREAKPOINT;
    }
    function hideBorders() {
      if (indexContent) indexContent.classList.add('index_content_no_borders');
      if (footerContent) footerContent.classList.add('footer_content_no_borders');
      if (header) header.classList.add('header_no_borders');
    }
    function showBorders() {
      if (indexContent) indexContent.classList.remove('index_content_no_borders');
      if (footerContent) footerContent.classList.remove('footer_content_no_borders');
      if (header) header.classList.remove('header_no_borders');
    }
    if (localStorage.getItem('borders') !== 'true' || isNarrow()) {
      hideBorders();
    }
    window.addEventListener('resize', function () {
      if (isNarrow()) {
        hideBorders();
      } else if (localStorage.getItem('borders') === 'true') {
        showBorders();
      }
    });
    document.addEventListener('keydown', function (e) {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const tag = document.activeElement.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA') return;
      if (e.key === 'b') {
        e.preventDefault();
        if (isNarrow()) return;
        let hidden;
        if (indexContent) hidden = indexContent.classList.toggle('index_content_no_borders');
        if (footerContent) hidden = footerContent.classList.toggle('footer_content_no_borders');
        if (header) header.classList.toggle('header_no_borders');
        localStorage.setItem('borders', hidden ? 'false' : 'true');
      }
    });
  })();

  (function () {
    function activateHeading(id) {
      const prev = document.querySelector('.heading_active');
      if (prev) prev.classList.remove('heading_active');
      if (!id) return;
      const el = document.getElementById(id);
      if (el) el.classList.add('heading_active');
    }

    function syncHash() {
      const hash = window.location.hash.substring(1);
      activateHeading(hash);
    }

    syncHash();
    window.addEventListener('hashchange', syncHash);

    document.addEventListener('click', function (e) {
      var link = e.target.closest('.toc_menu_item');
      if (!link) return;
      var href = link.getAttribute('href');
      if (!href || !href.startsWith('#')) return;
      e.preventDefault();
      var id = href.substring(1);
      var target = document.getElementById(id);
      if (target) target.scrollIntoView();
      history.replaceState(null, '', window.location.pathname + window.location.search + href);
      activateHeading(id);
    });
  })();

  (function () {
    let lastKey = '';
    let lastKeyTime = 0;

    function isEditable() {
      const tag = document.activeElement.tagName;
      return tag === 'INPUT' || tag === 'TEXTAREA';
    }

    document.addEventListener('keydown', function (e) {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (isEditable()) return;

      if (e.key === '\\' && (window.location.search || window.location.hash)) {
        e.preventDefault();
        if (window.location.search) {
          const searchInput = document.querySelector('.search_input');
          if (searchInput) searchInput.value = '';
          history.replaceState(null, '', window.location.pathname + window.location.hash);
          window.location.reload();
        } else {
          const prev = document.querySelector('.heading_active');
          if (prev) prev.classList.remove('heading_active');
          history.replaceState(null, '', window.location.pathname);
        }
        return;
      }

      const now = Date.now();
      const elapsed = now - lastKeyTime;

      if (e.key === 'j') {
        e.preventDefault();
        const menuPanel = document.querySelector('.site_menu.open .menu_panel_active');
        if (menuPanel) {
          menuPanel.scrollBy(0, 60);
        } else {
          window.scrollBy(0, 60);
        }
        lastKey = '';
        return;
      }

      if (e.key === 'k') {
        e.preventDefault();
        const menuPanel = document.querySelector('.site_menu.open .menu_panel_active');
        if (menuPanel) {
          menuPanel.scrollBy(0, -60);
        } else {
          window.scrollBy(0, -60);
        }
        lastKey = '';
        return;
      }

      if (e.key === 'G' && e.shiftKey) {
        e.preventDefault();
        window.scrollTo(0, document.body.scrollHeight);
        lastKey = '';
        return;
      }

      if (e.key === 'g') {
        if (lastKey === 'g' && elapsed < 400) {
          e.preventDefault();
          window.scrollTo(0, 0);
          lastKey = '';
          lastKeyTime = 0;
          return;
        }
        lastKey = 'g';
        lastKeyTime = now;
        return;
      }

      if (e.key === '[') {
        if (lastKey === '[' && elapsed < 400) {
          e.preventDefault();
          jumpHeading(-1);
          lastKey = '';
          lastKeyTime = 0;
          return;
        }
        lastKey = '[';
        lastKeyTime = now;
        return;
      }

      if (e.key === ']') {
        if (lastKey === ']' && elapsed < 400) {
          e.preventDefault();
          jumpHeading(1);
          lastKey = '';
          lastKeyTime = 0;
          return;
        }
        lastKey = ']';
        lastKeyTime = now;
        return;
      }

      lastKey = '';
    });

    function getHeadingId(h) {
      if (h.id) return h.id;
      const anchor = h.querySelector('.heading_anchor');
      if (anchor) {
        const href = anchor.getAttribute('href');
        if (href && href.startsWith('#')) return href.substring(1);
      }
      return '';
    }

    function jumpHeading(direction) {
      const container = document.querySelector('.content') || document.querySelector('.index_content') || document.querySelector('.container');
      if (!container) return;
      const headings = Array.from(container.querySelectorAll('h1, h2, h3, h4, h5, h6'));
      if (headings.length === 0) return;

      const scrollY = window.scrollY;
      const offset = 82;
      let target = null;

      if (direction === 1) {
        for (let i = 0; i < headings.length; i++) {
          if (headings[i].getBoundingClientRect().top + window.scrollY > scrollY + offset) {
            target = headings[i];
            break;
          }
        }
      } else {
        for (let i = headings.length - 1; i >= 0; i--) {
          if (headings[i].getBoundingClientRect().top < 70) {
            target = headings[i];
            break;
          }
        }
      }

      if (!target) return;
      target.scrollIntoView();
      const id = getHeadingId(target);
      if (id) {
        history.replaceState(null, '', window.location.pathname + '#' + id);
        const prev = document.querySelector('.heading_active');
        if (prev) prev.classList.remove('heading_active');
        target.classList.add('heading_active');
      }
    }
  })();

  (function () {
    const container = document.querySelector('.content') || document.querySelector('.index_content') || document.querySelector('.container');
    if (!container) return;
    const headings = Array.from(container.querySelectorAll('h1, h2, h3, h4, h5, h6'));
    if (headings.length === 0) return;

    function getActiveHeadingId() {
      let active = null;
      for (let i = 0; i < headings.length; i++) {
        if (headings[i].getBoundingClientRect().top <= 78) {
          let id = headings[i].id;
          if (!id) {
            const anchor = headings[i].querySelector('.heading_anchor');
            if (anchor) {
              const href = anchor.getAttribute('href');
              if (href && href.startsWith('#')) id = href.substring(1);
            }
          }
          if (id) active = id;
        }
      }
      return active;
    }

    function updateActiveToc() {
      const activeId = getActiveHeadingId();
      document.querySelectorAll('.toc_menu_item').forEach(function (item) {
        const href = item.getAttribute('href');
        const itemId = href ? href.substring(1) : '';
        item.classList.toggle('toc_menu_item_active', itemId === activeId);
      });
    }

    window.addEventListener('scroll', updateActiveToc, { passive: true });
    updateActiveToc();
  })();

  (function () {
    var blocks = document.querySelectorAll('details.collapsable_code[data-collapse-key]');
    if (blocks.length === 0) return;
    blocks.forEach(function (details) {
      var key = 'collapse_' + details.dataset.collapseKey;
      var saved = localStorage.getItem(key);
      if (saved === 'closed') details.removeAttribute('open');
      else if (saved === 'open') details.setAttribute('open', '');
      details.addEventListener('toggle', function () {
        localStorage.setItem(key, details.open ? 'open' : 'closed');
      });
    });
  })();
})();
