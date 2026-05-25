(function () {
  const input = document.querySelector('.search_input');
  const results = document.querySelector('.search_results');
  const clearBtn = document.querySelector('.search_clear');
  let fuse;
  const typedEl = document.querySelector('.title_typed');
  const cursorWrapEl = document.querySelector('.title_cursor_wrapper');
  const ghostEl = document.querySelector('.title_ghost');

  function collapseTitle() {
    if (typedEl) typedEl.style.display = 'none';
    if (cursorWrapEl) cursorWrapEl.style.display = 'none';
    if (ghostEl) ghostEl.style.display = 'none';
  }

  function expandTitle() {
    if (input.value.trim().length > 0) return;
    if (!input.classList.contains('search_input_expanded')) {
      if (typedEl) typedEl.style.display = '';
      if (cursorWrapEl) cursorWrapEl.style.display = '';
      if (ghostEl) ghostEl.style.display = '';
    }
  }

  input.addEventListener('focus', function () {
    collapseTitle();
    if (input.value.trim().length > 0 && results.innerHTML.length > 0) {
      results.style.display = 'block';
    }
  });
  input.addEventListener('blur', function () {
    expandTitle();
    setTimeout(function () {
      if (!input.closest('.search').contains(document.activeElement)) {
        results.style.display = 'none';
      }
    }, 0);
  });

  let searchData;
  fetch('/index.json')
    .then(function (r) { return r.json(); })
    .then(function (data) {
      searchData = data;
      fuse = new Fuse(data, {
        keys: ['title', 'content'],
        threshold: 0.3,
        ignoreLocation: true,
        includeMatches: true
      });
      if (input.value.trim().length > 0) {
        input.dispatchEvent(new Event('input'));
        results.style.display = 'none';
      }
    });

  function findNearestHeading(page, matchPos) {
    if (!page.headings || page.headings.length === 0) return null;
    const content = page.content;
    let best = null;
    let searchFrom = 0;
    for (let i = 0; i < page.headings.length; i++) {
      const h = page.headings[i];
      const pos = content.indexOf(h.title, searchFrom);
      if (pos === -1) continue;
      if (pos <= matchPos) {
        best = h;
      }
      searchFrom = pos + h.title.length;
    }
    return best;
  }

  input.addEventListener('input', function () {
    const query = input.value.trim();
    input.classList.toggle('search_input_expanded', query.length > 0);
    if (clearBtn) clearBtn.classList.toggle('search_clear_visible', query.length > 0);
    if (!query || !fuse) {
      results.style.display = 'none';
      results.innerHTML = '';
      return;
    }
    const matches = fuse.search(query);
    if (matches.length === 0) {
      results.style.display = 'block';
      results.innerHTML = '<div class="search_no_results">no results</div>';
      return;
    }
    results.style.display = 'block';
    results.innerHTML = matches.map(function (m) {
      const hits = [];
      const esc = function (s) { return s.replace(/&/g, '&amp;').replace(/</g, '&lt;'); };
      const dec = function (s) { const el = document.createElement('textarea'); el.innerHTML = s; return el.value; };
      const content = dec(m.item.content);
      const lowerContent = content.toLowerCase();
      const lowerQuery = query.toLowerCase();
      let searchPos = 0;
      while (true) {
        const pos = lowerContent.indexOf(lowerQuery, searchPos);
        if (pos === -1) break;
        const radius = 80;
        const start = Math.max(0, pos - radius);
        const end = Math.min(content.length, pos + query.length + radius);
        const slice = content.substring(start, end);
        const relStart = pos - start;
        const relEnd = relStart + query.length;
        const before = esc(slice.substring(0, relStart)).replace(/\n/g, '<br>');
        const matched = esc(slice.substring(relStart, relEnd)).replace(/\n/g, '<br>');
        const after = esc(slice.substring(relEnd)).replace(/\n/g, '<br>');
        const prefix = start > 0 ? '...' : '';
        const suffix = end < content.length ? '...' : '';
        const heading = findNearestHeading(m.item, pos);
        const hitUrl = heading ? m.item.url + '#' + heading.id : m.item.url;
        hits.push({ html: prefix + before + '<mark>' + matched + '</mark>' + after + suffix, url: hitUrl });
        searchPos = pos + query.length;
      }
      if (hits.length === 0 && m.matches) {
        for (let i = 0; i < m.matches.length; i++) {
          const match = m.matches[i];
          if (match.key !== 'content' || match.indices.length === 0) continue;
          const words = query.toLowerCase().split(/\s+/);
          for (let j = 0; j < match.indices.length; j++) {
            const idx = match.indices[j];
            const matchText = content.substring(idx[0], idx[1] + 1).toLowerCase();
            const hasWord = words.some(function (w) { return matchText.indexOf(w) !== -1; });
            if (!hasWord || idx[1] - idx[0] < 3) continue;
            let ls = content.lastIndexOf('\n', idx[0]);
            ls = ls === -1 ? 0 : ls + 1;
            let le = content.indexOf('\n', idx[1]);
            if (le === -1) le = content.length;
            const ctx = content.substring(ls, le).trim();
            const rs = idx[0] - ls;
            const re = idx[1] - ls + 1;
            const heading = findNearestHeading(m.item, idx[0]);
            const hitUrl = heading ? m.item.url + '#' + heading.id : m.item.url;
            hits.push({ html: '...' + esc(ctx.substring(0, rs)) + '<mark>' + esc(ctx.substring(rs, re)) + '</mark>' + esc(ctx.substring(re)) + '...', url: hitUrl });
          }
          break;
        }
      }
      if (hits.length === 0) return '';
      const hitLinks = hits.map(function (hit) {
        return '<a class="search_hit" href="' + hit.url + '">' + hit.html + '</a>';
      }).join('');
      return '<div class="search_result">' +
        '<div class="search_path"><span class="search_prompt">$</span> grep -i <span class="search_query">"' + esc(query) + '"</span> ./' + esc(m.item.title) + '</div>' +
        '<div class="search_matches">' + hits.length + ' match' + (hits.length !== 1 ? 'es' : '') + '</div>' +
        hitLinks +
        '</div>';
    }).join('');
    if (!results.innerHTML.trim()) {
      results.innerHTML = '<div class="search_no_results">no results</div>';
    }
  });

  results.addEventListener('click', function (e) {
    const link = e.target.closest('.search_hit');
    if (link) {
      e.preventDefault();
      const href = link.getAttribute('href');
      const q = input.value.trim();
      results.style.display = 'none';
      const url = new URL(href, window.location.origin);
      url.searchParams.set('q', q);
      window.location.href = url.toString();
    }
  });

  document.addEventListener('click', function (e) {
    if (!e.target.closest('.search')) {
      results.style.display = 'none';
    }
  });

  let activeIdx = -1;
  function updateActive() {
    const items = results.querySelectorAll('.search_hit');
    items.forEach(function (el, i) {
      el.classList.toggle('search_hit_active', i === activeIdx);
    });
    if (items[activeIdx]) items[activeIdx].scrollIntoView({ block: 'nearest' });
  }

  input.addEventListener('input', function () { activeIdx = -1; });

  function handleNav(e) {
    const items = results.querySelectorAll('.search_hit');
    if (items.length === 0) return;
    if (e.key === 'ArrowDown' || (e.key === 'Tab' && !e.shiftKey)) {
      e.preventDefault();
      activeIdx = (activeIdx + 1) % items.length;
      updateActive();
      items[activeIdx].focus();
    } else if (e.key === 'ArrowUp' || (e.key === 'Tab' && e.shiftKey)) {
      e.preventDefault();
      activeIdx = (activeIdx - 1 + items.length) % items.length;
      updateActive();
      items[activeIdx].focus();
    } else if (e.key === 'Enter' && activeIdx >= 0 && items[activeIdx]) {
      e.preventDefault();
      const href = items[activeIdx].getAttribute('href');
      const q = input.value.trim();
      results.style.display = 'none';
      const url = new URL(href, window.location.origin);
      url.searchParams.set('q', q);
      window.location.href = url.toString();
    } else if (e.key === 'Escape') {
      results.style.display = 'none';
      activeIdx = -1;
      input.focus();
    }
  }

  input.addEventListener('keydown', function (e) {
    if (e.ctrlKey && e.key === 'a') {
      e.preventDefault();
      input.setSelectionRange(0, 0);
    } else if (e.ctrlKey && e.key === 'e') {
      e.preventDefault();
      input.setSelectionRange(input.value.length, input.value.length);
    } else if (e.ctrlKey && e.key === 'k') {
      e.preventDefault();
      const pos = input.selectionStart;
      input.value = input.value.substring(0, pos);
      input.dispatchEvent(new Event('input'));
    } else if (e.ctrlKey && e.key === 'w') {
      e.preventDefault();
      const pos = input.selectionStart;
      const before = input.value.substring(0, pos);
      const after = input.value.substring(pos);
      const trimmed = before.replace(/\s*\S+\s*$/, '');
      input.value = trimmed + after;
      input.setSelectionRange(trimmed.length, trimmed.length);
      input.dispatchEvent(new Event('input'));
    }
  });

  input.addEventListener('keydown', handleNav);
  results.addEventListener('keydown', handleNav);

  function clearSearch() {
    input.value = '';
    input.classList.remove('search_input_expanded');
    if (clearBtn) clearBtn.classList.remove('search_clear_visible');
    results.style.display = 'none';
    results.innerHTML = '';
    activeIdx = -1;
    input.focus();
  }

  if (clearBtn) {
    clearBtn.addEventListener('mousedown', function (e) {
      e.preventDefault();
      clearSearch();
    });
  }

  document.addEventListener('keydown', function (e) {
    if (e.key === '/' && document.activeElement !== input && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      input.focus();
    }
    if (e.key === 'u' && e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      clearSearch();
    }
  });
  (function () {
    const params = new URLSearchParams(window.location.search);
    const q = params.get('q');
    if (q) {
      input.classList.add('search_input_instant');
      input.value = q;
      input.classList.add('search_input_expanded');
      if (clearBtn) clearBtn.classList.add('search_clear_visible');
      collapseTitle();
      requestAnimationFrame(function () { input.classList.remove('search_input_instant'); });
    }
  })();

  function highlightSearch() {
    const params = new URLSearchParams(window.location.search);
    const highlight = params.get('q');
    if (highlight && typeof Mark !== 'undefined') {
      const ctx = document.querySelector('.index_content') || document.querySelector('.content') || document.querySelector('.container');
      if (ctx) {
        const marker = new Mark(ctx);
        marker.mark(highlight, {
          className: 'search_highlight',
          separateWordSearch: false,
          acrossElements: true
        });
      }
    }
  }
  let matchIdx = -1;
  function jumpMatch(direction) {
    const marks = document.querySelectorAll('.search_highlight');
    if (marks.length === 0) return;
    marks.forEach(function (m) { m.classList.remove('search_highlight_active'); });
    if (direction === 1) {
      matchIdx = (matchIdx + 1) % marks.length;
    } else {
      matchIdx = (matchIdx - 1 + marks.length) % marks.length;
    }
    marks[matchIdx].classList.add('search_highlight_active');
    marks[matchIdx].scrollIntoView({ block: 'center' });
  }

  function activateNearestMark() {
    const hash = window.location.hash;
    if (!hash) return;
    const target = document.getElementById(hash.substring(1));
    if (!target) return;
    const marks = document.querySelectorAll('.search_highlight');
    if (marks.length === 0) return;
    const targetTop = target.getBoundingClientRect().top + window.scrollY;
    let bestIdx = 0;
    let bestDist = Infinity;
    marks.forEach(function (m, i) {
      const dist = Math.abs(m.getBoundingClientRect().top + window.scrollY - targetTop);
      if (dist < bestDist) {
        bestDist = dist;
        bestIdx = i;
      }
    });
    matchIdx = bestIdx;
    marks[matchIdx].classList.add('search_highlight_active');
    marks[matchIdx].scrollIntoView({ block: 'center' });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { highlightSearch(); activateNearestMark(); });
  } else {
    highlightSearch();
    activateNearestMark();
  }

  document.addEventListener('keydown', function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const tag = document.activeElement.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA') return;
    if (e.key === 'n' && !e.shiftKey) {
      const marks = document.querySelectorAll('.search_highlight');
      if (marks.length > 0) {
        e.preventDefault();
        jumpMatch(1);
      }
    } else if (e.key === 'N' && e.shiftKey) {
      const marks = document.querySelectorAll('.search_highlight');
      if (marks.length > 0) {
        e.preventDefault();
        jumpMatch(-1);
      }
    }
  });
})();
