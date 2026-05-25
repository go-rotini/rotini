(function () {
  const left = document.querySelector('.sidebar_left');
  const right = document.querySelector('.sidebar_right');
  if (!left || !right) return;

  const headerInner = document.querySelector('.header_inner');
  function setHeaderHeight() {
    if (!headerInner) return;
    const h = headerInner.offsetHeight + 'px';
    document.documentElement.style.setProperty('--header-height', h);
  }
  setHeaderHeight();
  window.addEventListener('resize', setHeaderHeight);

  const sidebarMinWidth = 400;
  const sidebarPadding = 40;
  const needed = sidebarMinWidth * 2 + sidebarPadding;

  function getContainerWidth() {
    const val = getComputedStyle(document.documentElement).getPropertyValue('--container-width').trim();
    return parseInt(val, 10) || 900;
  }

  const container = document.querySelector('.container');
  let zenMode = localStorage.getItem('zen') === 'true';

  if (localStorage.getItem('sidebar_borders') === 'false') {
    left.classList.add('sidebar_no_borders');
    right.classList.add('sidebar_no_borders');
  }

  const defaults = { theme: 'system', text: 'medium', width: 'medium' };
  const widthMap = { small: '700px', medium: '900px', large: '1100px' };
  const textMap = { small: '0.8rem', medium: '0.9rem', large: '1rem' };

  function loadPrefs() {
    const prefs = {};
    for (const key in defaults) {
      prefs[key] = localStorage.getItem(key) || defaults[key];
    }
    return prefs;
  }

  function applyPrefs(prefs) {
    document.documentElement.style.setProperty('--container-width', widthMap[prefs.width] || widthMap.medium);
    document.documentElement.style.setProperty('--font-size', textMap[prefs.text] || textMap.medium);

    const root = document.documentElement;
    if (prefs.theme === 'light') {
      root.setAttribute('data-theme', 'light');
    } else if (prefs.theme === 'dark') {
      root.setAttribute('data-theme', 'dark');
    } else {
      root.removeAttribute('data-theme');
    }

    document.querySelectorAll('.sidebar .prefs_option').forEach(function (btn) {
      const isActive = prefs[btn.dataset.pref] === btn.dataset.value;
      btn.classList.toggle('prefs_option_active', isActive);
    });

    const menuDropdown = document.querySelector('.site_menu_dropdown');
    if (menuDropdown) {
      menuDropdown.querySelectorAll('.prefs_option').forEach(function (btn) {
        const isActive = prefs[btn.dataset.pref] === btn.dataset.value;
        btn.classList.toggle('prefs_option_active', isActive);
      });
    }
  }

  const prefs = loadPrefs();
  applyPrefs(prefs);

  function update() {
    if (zenMode) return;
    const menuOpen = !!document.querySelector('.site_menu.open');
    const available = window.innerWidth - getContainerWidth();
    const show = available >= needed && !menuOpen;
    left.classList.toggle('sidebar_visible', show);
    right.classList.toggle('sidebar_visible', show);
    if (container) container.classList.toggle('container_no_hpad', show);
    if (show) {
      const current = loadPrefs();
      for (const k in current) prefs[k] = current[k];
      document.querySelectorAll('.sidebar .prefs_option').forEach(function (btn) {
        btn.classList.toggle('prefs_option_active', prefs[btn.dataset.pref] === btn.dataset.value);
      });
    }
  }

  update();
  window.addEventListener('resize', update);

  const observer = new MutationObserver(update);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['style', 'data-theme'] });

  const path = window.location.pathname.replace(/\/+$/, '') || '/';
  left.querySelectorAll('.sidebar_nav .ls_row').forEach(function (row) {
    const href = row.getAttribute('href').replace(/\/+$/, '') || '/';
    if (href === path) {
      row.querySelector('.ls_name').classList.add('ls_name_active');
      row.addEventListener('click', function (e) { e.preventDefault(); window.scrollTo(0, 0); });
    }
  });

  function buildSidebarToc() {
    const tocEl = left.querySelector('.sidebar_toc');
    if (!tocEl) return;
    const cnt = document.querySelector('.content') || document.querySelector('.index_content') || document.querySelector('.container');
    if (!cnt) return;
    const headings = cnt.querySelectorAll('h1, h2, h3, h4, h5, h6');
    if (headings.length === 0) {
      tocEl.innerHTML = '<div class="toc_menu_empty">no headings</div>';
      return;
    }
    let html = '';
    headings.forEach(function (h) {
      const level = parseInt(h.tagName[1]);
      let id = h.id || (h.querySelector('a[id]') ? h.querySelector('a[id]').id : '');
      if (!id) {
        const anchor = h.querySelector('.heading_anchor');
        if (anchor) {
          const href = anchor.getAttribute('href');
          if (href && href.startsWith('#')) id = href.substring(1);
        }
      }
      const text = h.textContent.replace(/^#\s*/, '').trim();
      const indent = (level - 1) * 12;
      html += '<a class="toc_menu_item" href="#' + id + '" style="padding-left:' + (10 + indent) + 'px">' + text + '</a>';
    });
    tocEl.innerHTML = html;
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', buildSidebarToc);
  } else {
    buildSidebarToc();
  }

  right.addEventListener('click', function (e) {
    const btn = e.target.closest('.prefs_option');
    if (!btn) return;
    const key = btn.dataset.pref;
    const value = btn.dataset.value;
    prefs[key] = value;
    localStorage.setItem(key, value);
    applyPrefs(prefs);
    update();
  });

  const sidebarSections = {
    n: left.querySelector('[data-sidebar-key="n"]'),
    c: left.querySelector('[data-sidebar-key="c"]'),
    p: right.querySelector('[data-sidebar-key="p"]')
  };

  function focusSidebarSection(key) {
    const label = sidebarSections[key];
    if (!label) return;
    const section = label.closest('.sidebar_section');
    if (!section) return;
    const first = section.querySelector('.ls_row, .toc_menu_item, .prefs_option, .hotkeys_row');
    if (first && first.focus) {
      first.focus();
    } else {
      section.scrollIntoView({ block: 'nearest' });
    }
  }

  document.addEventListener('keydown', function (e) {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp' && e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    const el = document.activeElement;
    if (!el || !el.closest('.sidebar')) return;

    const prefsBtn = el.closest('.prefs_option');
    if (prefsBtn) {
      const group = prefsBtn.closest('.prefs_group');
      const groups = Array.from(prefsBtn.closest('.prefs_menu_content').querySelectorAll('.prefs_group'));
      const siblings = Array.from(group.querySelectorAll('.prefs_option'));
      const colIdx = siblings.indexOf(prefsBtn);
      const rowIdx = groups.indexOf(group);

      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
        e.preventDefault();
        const newCol = e.key === 'ArrowRight'
          ? (colIdx + 1) % siblings.length
          : (colIdx - 1 + siblings.length) % siblings.length;
        siblings[newCol].focus();
      } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const newRow = e.key === 'ArrowDown'
          ? (rowIdx + 1) % groups.length
          : (rowIdx - 1 + groups.length) % groups.length;
        const targetBtns = Array.from(groups[newRow].querySelectorAll('.prefs_option'));
        targetBtns[Math.min(colIdx, targetBtns.length - 1)].focus();
      }
      return;
    }

    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const section = el.closest('.sidebar_section');
      if (!section) return;
      const items = Array.from(section.querySelectorAll('.ls_row, .toc_menu_item'));
      if (items.length === 0) return;
      e.preventDefault();
      const idx = items.indexOf(el);
      if (idx === -1) {
        items[e.key === 'ArrowDown' ? 0 : items.length - 1].focus();
      } else if (e.key === 'ArrowDown') {
        items[(idx + 1) % items.length].focus();
      } else {
        items[(idx - 1 + items.length) % items.length].focus();
      }
    }
  });

  document.addEventListener('keydown', function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const tag = document.activeElement.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA') return;
    if (!left.classList.contains('sidebar_visible')) return;
    if (e.key === 'n' || e.key === 'c' || e.key === 'p') {
      const marks = document.querySelectorAll('.search_highlight');
      if ((e.key === 'n' || e.key === 'N') && marks.length > 0) return;
      if (document.querySelector('.site_menu.open')) return;
      e.preventDefault();
      focusSidebarSection(e.key);
    }
  });

  document.addEventListener('keydown', function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const tag = document.activeElement.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA') return;
    if (e.key === 'v') {
      e.preventDefault();
      const hidden = left.classList.toggle('sidebar_no_borders');
      right.classList.toggle('sidebar_no_borders');
      localStorage.setItem('sidebar_borders', hidden ? 'false' : 'true');
    }
    if (e.key === 'z') {
      e.preventDefault();
      zenMode = !zenMode;
      localStorage.setItem('zen', zenMode);
      if (zenMode) {
        left.classList.remove('sidebar_visible');
        right.classList.remove('sidebar_visible');
        if (container) container.classList.remove('container_no_hpad');
      } else {
        update();
      }
    }
  });
})();
