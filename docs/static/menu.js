(function () {
  const trigger = document.querySelector('.site_menu_trigger');
  if (!trigger) return;
  const menu = trigger.closest('.site_menu');
  const dropdown = menu.querySelector('.site_menu_dropdown');
  const closeBtn = dropdown.querySelector('.menu_header_close');
  const list = dropdown.querySelector('.toc_menu_list');
  const tabs = dropdown.querySelectorAll('.menu_tab');
  const panels = dropdown.querySelectorAll('.menu_panel');
  const statusMode = dropdown.querySelector('.menu_statusline_mode');
  const statusInfo = dropdown.querySelector('.menu_statusline_info');

  const path = window.location.pathname.replace(/\/+$/, '') || '/';
  dropdown.querySelectorAll('[data-panel="n"] .ls_row').forEach(function (row) {
    const href = row.getAttribute('href').replace(/\/+$/, '') || '/';
    if (href === path) {
      row.querySelector('.ls_name').classList.add('ls_name_active');
      row.addEventListener('click', function (e) { e.preventDefault(); close(); window.scrollTo(0, 0); });
    }
  });

  const tabModes = { n: 'NAVIGATE', c: 'CONTENTS', p: 'PREFERENCES', h: 'HOTKEYS' };

  function updateStatusline(tabName) {
    statusMode.textContent = '-- ' + tabModes[tabName] + ' --';
    if (tabName === 'n') {
      const rows = dropdown.querySelectorAll('[data-panel="n"] .ls_row');
      statusInfo.textContent = rows.length + 'L, ' + rows.length + ' items';
    } else if (tabName === 'c') {
      const items = dropdown.querySelectorAll('.toc_menu_item');
      statusInfo.textContent = items.length + 'L, ' + items.length + ' headings';
    } else if (tabName === 'p') {
      statusInfo.textContent = '3 groups, 9 options';
    } else if (tabName === 'h') {
      const rows = dropdown.querySelectorAll('[data-panel="h"] .hotkeys_row');
      statusInfo.textContent = rows.length + ' bindings';
    }
  }

  function switchTab(tabName) {
    tabs.forEach(function (t) { t.classList.toggle('menu_tab_active', t.dataset.tab === tabName); });
    panels.forEach(function (p) { p.classList.toggle('menu_panel_active', p.dataset.panel === tabName); });
    if (tabName === 'c') buildToc();
    updateStatusline(tabName);
    dropdown.focus();
  }

  dropdown.addEventListener('click', function (e) {
    const tab = e.target.closest('.menu_tab');
    if (tab) switchTab(tab.dataset.tab);
  });

  function buildToc() {
    const container = document.querySelector('.content') || document.querySelector('.index_content') || document.querySelector('.container');
    if (!container) return;
    const headings = container.querySelectorAll('h1, h2, h3, h4, h5, h6');
    if (headings.length === 0) {
      list.innerHTML = '<div class="toc_menu_empty">no headings</div>';
      return;
    }
    let html = '';
    headings.forEach(function (h) {
      const level = parseInt(h.tagName[1]);
      let id = h.id || h.querySelector('a[id]')?.id || '';
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
    list.innerHTML = html;
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

    const fontSize = textMap[prefs.text] || textMap.medium;
    document.documentElement.style.setProperty('--font-size', fontSize);

    const root = document.documentElement;
    if (prefs.theme === 'light') {
      root.setAttribute('data-theme', 'light');
    } else if (prefs.theme === 'dark') {
      root.setAttribute('data-theme', 'dark');
    } else {
      root.removeAttribute('data-theme');
    }

    dropdown.querySelectorAll('.prefs_option').forEach(function (btn) {
      const isActive = prefs[btn.dataset.pref] === btn.dataset.value;
      btn.classList.toggle('prefs_option_active', isActive);
    });
  }

  function savePref(key, value) {
    localStorage.setItem(key, value);
  }

  const prefs = loadPrefs();
  applyPrefs(prefs);

  window.addEventListener('resize', function () {
    const current = loadPrefs();
    for (const k in current) prefs[k] = current[k];
    applyPrefs(prefs);
  });

  dropdown.addEventListener('click', function (e) {
    const btn = e.target.closest('.prefs_option');
    if (!btn) return;
    const key = btn.dataset.pref;
    const value = btn.dataset.value;
    prefs[key] = value;
    savePref(key, value);
    applyPrefs(prefs);
  });

  function hideSidebars() {
    document.querySelectorAll('.sidebar').forEach(function (s) {
      s.classList.remove('sidebar_visible');
    });
  }

  function showSidebars() {
    const container = document.querySelector('.container');
    if (container) container.classList.remove('container_no_hpad');
    const event = new Event('resize');
    window.dispatchEvent(event);
  }

  function toggle() {
    const open = menu.classList.toggle('open');
    trigger.setAttribute('aria-expanded', open);
    if (open) {
      hideSidebars();
      const activeTab = dropdown.querySelector('.menu_tab_active');
      const activeKey = activeTab ? activeTab.dataset.tab : 'n';
      if (activeKey === 'c') buildToc();
      updateStatusline(activeKey);
      dropdown.focus();
    } else {
      showSidebars();
    }
  }

  function close() {
    if (!menu.classList.contains('open')) return;
    menu.classList.remove('open');
    trigger.setAttribute('aria-expanded', 'false');
    showSidebars();
  }

  trigger.addEventListener('click', toggle);
  closeBtn.addEventListener('click', function () { close(); trigger.focus(); });

  trigger.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      toggle();
    }
  });

  list.addEventListener('click', function (e) {
    const item = e.target.closest('.toc_menu_item');
    if (item) close();
  });

  dropdown.addEventListener('keydown', function (e) {
    if (e.key === 'x' && !e.ctrlKey && !e.metaKey && !e.altKey && document.activeElement.tagName !== 'INPUT' && document.activeElement.tagName !== 'TEXTAREA') {
      e.preventDefault();
      close();
      trigger.focus();
      return;
    }
    if ((e.key === 'n' || e.key === 'c' || e.key === 'p' || e.key === 'h') && !e.ctrlKey && !e.metaKey && !e.altKey && document.activeElement.tagName !== 'INPUT' && document.activeElement.tagName !== 'TEXTAREA') {
      e.preventDefault();
      switchTab(e.key);
      return;
    }
    if (e.key === 'Escape') {
      close();
      trigger.focus();
      return;
    }
    if (e.key === 'Tab') {
      const activePanel = dropdown.querySelector('.menu_panel_active');
      const headerItems = Array.from(dropdown.querySelectorAll('.menu_tab, .menu_header_close'));
      const panelItems = activePanel ? Array.from(activePanel.querySelectorAll('.ls_row, .toc_menu_item, .prefs_option')) : [];
      const focusable = headerItems.concat(panelItems);
      if (focusable.length === 0) return;
      const idx = focusable.indexOf(document.activeElement);
      if (!e.shiftKey && (idx === focusable.length - 1 || idx === -1)) {
        e.preventDefault();
        focusable[0].focus();
      } else if (e.shiftKey && idx <= 0) {
        e.preventDefault();
        focusable[focusable.length - 1].focus();
      }
      return;
    }

    if (e.key === 'ArrowDown' && (document.activeElement === dropdown || document.activeElement.closest('.menu_tab') || document.activeElement.closest('.menu_header_close'))) {
      e.preventDefault();
      const activePanel = dropdown.querySelector('.menu_panel_active');
      if (!activePanel) return;
      const first = activePanel.querySelector('.ls_row, .toc_menu_item, .prefs_option');
      if (first) first.focus();
      return;
    }

    const btn = document.activeElement.closest('.prefs_option');
    if (btn) {
      const group = btn.closest('.prefs_group');
      const groups = Array.from(dropdown.querySelectorAll('.prefs_group'));
      const siblings = Array.from(group.querySelectorAll('.prefs_option'));
      const colIdx = siblings.indexOf(btn);
      const rowIdx = groups.indexOf(group);

      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
        e.preventDefault();
        const newCol = e.key === 'ArrowRight'
          ? (colIdx + 1) % siblings.length
          : (colIdx - 1 + siblings.length) % siblings.length;
        siblings[newCol].focus();
        return;
      } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const newRow = e.key === 'ArrowDown'
          ? (rowIdx + 1) % groups.length
          : (rowIdx - 1 + groups.length) % groups.length;
        const targetButtons = Array.from(groups[newRow].querySelectorAll('.prefs_option'));
        const targetCol = Math.min(colIdx, targetButtons.length - 1);
        targetButtons[targetCol].focus();
        return;
      }
    }

    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && e.metaKey) {
      const activePanel = dropdown.querySelector('.menu_panel_active');
      if (!activePanel) return;
      e.preventDefault();
      activePanel.scrollTo({ top: e.key === 'ArrowDown' ? activePanel.scrollHeight : 0, behavior: 'smooth' });
      return;
    }

    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const activePanel = dropdown.querySelector('.menu_panel_active');
      if (!activePanel) return;
      const items = Array.from(activePanel.querySelectorAll('.ls_row, .toc_menu_item'));
      if (items.length === 0) {
        e.preventDefault();
        activePanel.scrollBy(0, e.key === 'ArrowDown' ? 40 : -40);
        return;
      }
      e.preventDefault();
      const idx = items.indexOf(document.activeElement);
      if (idx === -1) {
        items[e.key === 'ArrowDown' ? 0 : items.length - 1].focus();
      } else if (e.key === 'ArrowDown') {
        items[(idx + 1) % items.length].focus();
      } else {
        items[(idx - 1 + items.length) % items.length].focus();
      }
    }
  });

  document.addEventListener('click', function (e) {
    if (!e.target.closest('.site_menu')) close();
  });

  menu.addEventListener('focusout', function (e) {
    setTimeout(function () {
      if (!menu.contains(document.activeElement)) close();
    }, 0);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === '?' && !e.ctrlKey && !e.metaKey && document.activeElement.tagName !== 'INPUT' && document.activeElement.tagName !== 'TEXTAREA') {
      e.preventDefault();
      toggle();
    }
  });
})();
