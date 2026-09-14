// This page collects credentials once and stores a ready-made
// "Authorization: Basic ..." header value in sessionStorage. Every
// subsequent API call in app.js attaches that header itself, so the
// browser never shows its native Basic-auth prompt (the server never sends
// WWW-Authenticate on /api/*) and a real "log out" (clearing
// sessionStorage) becomes possible, which Basic auth normally can't do.
const form = document.getElementById('login-form');
const errorEl = document.getElementById('login-error');

form.addEventListener('submit', async (e) => {
  e.preventDefault();
  errorEl.hidden = true;
  const username = document.getElementById('username').value;
  const password = document.getElementById('password').value;

  let resp;
  try {
    resp = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
  } catch (err) {
    errorEl.textContent = 'Could not reach the device.';
    errorEl.hidden = false;
    return;
  }

  if (!resp.ok) {
    errorEl.textContent = resp.status === 429
      ? 'Too many failed attempts. Please wait and try again.'
      : 'Incorrect username or password.';
    errorEl.hidden = false;
    return;
  }

  const body = await resp.json();
  const authHeader = 'Basic ' + btoa(username + ':' + password);
  sessionStorage.setItem('rr_auth', authHeader);
  sessionStorage.setItem('rr_must_change', body.must_change ? '1' : '');
  location.href = 'index.html';
});
