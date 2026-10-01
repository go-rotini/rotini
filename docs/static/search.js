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

  // Search work is kept off the keystroke. Typing only schedules a search (DEBOUNCE_MS after
  // the last key), so the input always updates at once. Each page's text is decoded, lowered
  // and mapped to its headings ONCE when the index loads, and a search shows at most
  // MAX_HITS snippets per page: the specification page alone is ~140KB, and building a
  // snippet for every occurrence of a one-letter query used to freeze the page.
  const DEBOUNCE_MS = 120;
  const MAX_HITS = 5;
  const MAX_COUNT = 999;
  let pages = [];
  let pending;

  const esc = function (s) { return s.replace(/&/g, '&amp;').replace(/</g, '&lt;'); };
  const decoder = document.createElement('textarea');
  function decode(s) { decoder.innerHTML = s; return decoder.value; }

  // Each heading's position in the page text, in page order, so the heading above a hit is a
  // binary search rather than a rescan of the page.
  function headingPositions(page, text) {
    const out = [];
    let from = 0;
    (page.headings || []).forEach(function (h) {
      const pos = text.indexOf(h.title, from);
      if (pos === -1) return;
      out.push({ pos: pos, id: h.id });
      from = pos + h.title.length;
    });
    return out;
  }

  function hitURL(page, pos) {
    const hs = page.headingPos;
    let lo = 0, hi = hs.length - 1, best = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (hs[mid].pos <= pos) { best = mid; lo = mid + 1; } else { hi = mid - 1; }
    }
    return best >= 0 ? page.url + '#' + hs[best].id : page.url;
  }

  function snippet(text, pos, len) {
    const radius = 80;
    const start = Math.max(0, pos - radius);
    const end = Math.min(text.length, pos + len + radius);
    const slice = text.substring(start, end);
    const rs = pos - start;
    const br = function (s) { return esc(s).replace(/\n/g, '<br>'); };
    return (start > 0 ? '...' : '') + br(slice.substring(0, rs)) + '<mark>' +
      br(slice.substring(rs, rs + len)) + '</mark>' + br(slice.substring(rs + len)) +
      (end < text.length ? '...' : '');
  }

  // exactResults finds the query as typed (case-insensitively) — the grep the results present.
  function exactResults(query) {
    const lq = query.toLowerCase();
    const out = [];
    pages.forEach(function (page) {
      const hits = [];
      let count = 0;
      let from = 0;
      while (count < MAX_COUNT) {
        const pos = page.lower.indexOf(lq, from);
        if (pos === -1) break;
        if (hits.length < MAX_HITS) hits.push({ html: snippet(page.text, pos, query.length), url: hitURL(page, pos) });
        count++;
        from = pos + lq.length;
      }
      const inTitle = page.title.toLowerCase().indexOf(lq) !== -1;
      if (count > 0 || inTitle) out.push({ page: page, hits: hits, count: count, inTitle: inTitle });
    });
    out.sort(function (a, b) { return (b.inTitle - a.inTitle) || (b.count - a.count); });
    return out;
  }

  // fuzzyResults is the fallback for a query with no exact match, so a misspelling
  // ("enviornment") still finds the word it meant. Fuse marks the stretches of text that
  // matched; only those about as long as the query are real near-misses, the rest are
  // scattered letters.
  function fuzzyResults(query) {
    if (!fuse) return [];
    const minLen = Math.max(3, query.length - 2);
    return fuse.search(query).map(function (m) {
      const page = m.item;
      const ranges = [];
      (m.matches || []).forEach(function (match) {
        if (match.key !== 'text') return;
        match.indices.forEach(function (idx) {
          if (idx[1] - idx[0] + 1 >= minLen) ranges.push(idx);
        });
      });
      ranges.sort(function (x, y) { return x[0] - y[0]; });
      const hits = ranges.slice(0, MAX_HITS).map(function (idx) {
        return { html: snippet(page.text, idx[0], idx[1] - idx[0] + 1), url: hitURL(page, idx[0]) };
      });
      return { page: page, hits: hits, count: ranges.length, inTitle: false };
    }).filter(function (r) { return r.hits.length > 0; });
  }

  function render(query, found) {
    if (found.length === 0) {
      results.innerHTML = '<div class="search_no_results">no results</div>';
      return;
    }
    results.innerHTML = found.map(function (r) {
      const counted = r.count >= MAX_COUNT ? MAX_COUNT + '+' : String(r.count);
      const more = r.count > r.hits.length
        ? '<a class="search_hit search_more" href="' + r.page.url + '">... ' + (r.count >= MAX_COUNT ? 'many' : r.count - r.hits.length) + ' more on this page</a>'
        : '';
      return '<div class="search_result">' +
        '<div class="search_path"><span class="search_prompt">$</span> grep -i <span class="search_query">"' + esc(query) + '"</span> ./' + esc(r.page.title) + '</div>' +
        '<div class="search_matches">' + counted + ' match' + (r.count !== 1 ? 'es' : '') + '</div>' +
        r.hits.map(function (hit) { return '<a class="search_hit" href="' + hit.url + '">' + hit.html + '</a>'; }).join('') +
        more +
        '</div>';
    }).join('');
  }

  function runSearch() {
    const query = input.value.trim();
    if (!query || pages.length === 0) {
      results.style.display = 'none';
      results.innerHTML = '';
      return;
    }
    let found = exactResults(query);
    if (found.length === 0) found = fuzzyResults(query);
    render(query, found);
    if (document.activeElement === input) results.style.display = 'block';
  }

  fetch('/index.json')
    .then(function (r) { return r.json(); })
    .then(function (data) {
      pages = data.map(function (page) {
        const text = decode(page.content || '');
        return {
          title: page.title || '',
          url: page.url,
          text: text,
          lower: text.toLowerCase(),
          headingPos: headingPositions(page, text)
        };
      });
      fuse = new Fuse(pages, {
        keys: ['title', 'text'],
        threshold: 0.3,
        ignoreLocation: true,
        includeMatches: true
      });
      if (input.value.trim().length > 0) {
        runSearch();
        results.style.display = 'none';
      }
    });

  input.addEventListener('input', function () {
    const query = input.value.trim();
    input.classList.toggle('search_input_expanded', query.length > 0);
    if (clearBtn) clearBtn.classList.toggle('search_clear_visible', query.length > 0);
    clearTimeout(pending);
    if (!query) {
      results.style.display = 'none';
      results.innerHTML = '';
      return;
    }
    pending = setTimeout(runSearch, DEBOUNCE_MS);
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
    clearTimeout(pending);
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
