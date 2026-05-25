(function () {
  const blink = document.querySelector('.title_blink');
  const typed = document.querySelector('.title_typed');
  const ghost = document.querySelector('.title_ghost');
  const cursorChar = document.querySelector('.title_cursor_char');

  const titleEl = typed ? typed.closest('.title') : null;
  function showTitle() {
    if (titleEl) titleEl.classList.add('title_ready');
  }

  if (document.cookie.split('; ').some(function (c) { return c.startsWith('typing_seen='); })) {
    return;
  }

  if (typed) typed.textContent = '';
  document.documentElement.classList.remove('title_skip');

  function setTypingCookie() {
    const d = new Date();
    d.setTime(d.getTime() + 7 * 24 * 60 * 60 * 1000);
    document.cookie = 'typing_seen=1; expires=' + d.toUTCString() + '; path=/';
  }

  let blinkTimeout;
  function pauseBlink() {
    blink.style.animation = 'none';
    blink.style.opacity = '1';
    cursorChar.style.animation = 'none';
    cursorChar.style.color = 'var(--background)';
    cursorChar.style.opacity = '1';
    clearTimeout(blinkTimeout);
    blinkTimeout = setTimeout(function () {
      blink.style.animation = '';
      cursorChar.style.animation = '';
      cursorChar.style.color = '';
      cursorChar.style.opacity = '';
    }, 500);
  }

  function updateCursorChar(ghostText) {
    cursorChar.textContent = ghostText ? ghostText[0] : '';
  }
  const blinkHalf = 624;
  function setCursorDark() {
    cursorChar.style.color = 'var(--background)';
    cursorChar.style.opacity = '1';
  }
  function setCursorGhost() {
    cursorChar.style.color = 'var(--foreground-5)';
    cursorChar.style.opacity = '1';
  }
  function singleBlink(cb) {
    clearTimeout(blinkTimeout);
    blink.style.animation = 'none';
    blink.style.opacity = '1';
    cursorChar.style.animation = 'none';
    setCursorDark();
    setTimeout(function () {
      blink.style.opacity = '0';
      setCursorGhost();
      setTimeout(function () {
        blink.style.opacity = '1';
        setCursorDark();
        setTimeout(cb, blinkHalf);
      }, blinkHalf);
    }, blinkHalf);
  }

  function finalSingleBlink(cb) {
    blink.style.animation = 'none';
    blink.style.opacity = '0';
    cursorChar.style.animation = 'none';
    setCursorGhost();
    setTimeout(function () {
      blink.style.opacity = '1';
      setCursorDark();
      setTimeout(cb, blinkHalf);
    }, blinkHalf);
  }

  const steps = [
    { type: '\u00a0', delay: 400 },
    { type: '-', ghost: 'v', delay: 1000, blink: true },
    { type: '-', ghost: 'help', delay: 1000, blink: true },
    { type: 'd', ghost: 'ocumentation', delay: 480 },
    { type: 'o', ghost: 'cumentation', delay: 160 },
    { type: 'c', ghost: 'umentation', delay: 160 },
    { type: 'u', ghost: 'mentation', delay: 160 },
    { type: 'm', ghost: 'entation', delay: 160 },
    { type: 'e', ghost: 'ntation', delay: 160 },
    { type: 'n', ghost: 'tation', delay: 160 },
    { type: 't', ghost: 'ation', delay: 1000, blink: true },
    { type: 'ation', ghost: '', delay: 0 }
  ];
  showTitle();
  let blinkCount = 0;
  const blinkInterval = setInterval(function () {
    blinkCount++;
    if (blinkCount >= 3) {
      clearInterval(blinkInterval);
      blink.style.marginLeft = '0';
      let i = 0;
      function runStep() {
        if (i >= steps.length) return;
        const step = steps[i];
        pauseBlink();
        typed.textContent += step.type;
        if ('ghost' in step) {
          updateCursorChar(step.ghost);
          ghost.textContent = step.ghost.substring(1);
        }
        i++;
        if (i < steps.length) {
          if (step.blink) {
            singleBlink(runStep);
          } else {
            setTimeout(runStep, step.delay);
          }
        } else {
          let finalBlinks = 0;
          function finalBlink() {
            if (finalBlinks >= 4) {
              blink.style.animation = 'none';
              blink.style.opacity = '1';
              cursorChar.style.animation = 'none';
              setCursorDark();
              setTypingCookie();
              return;
            }
            finalBlinks++;
            finalSingleBlink(finalBlink);
          }
          finalBlink();
        }
      }
      runStep();
    }
  }, 1248);
})();
