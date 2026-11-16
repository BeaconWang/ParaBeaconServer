(() => {
  const TOKEN_KEY = "parabeacon-access-token";
  const USER_KEY = "parabeacon-account-name";
  let memoryToken = "";
  let memoryUser = "";

  const storage = (() => {
    try {
      const candidate = window.sessionStorage;
      const probe = "__parabeacon_storage_probe__";
      candidate.setItem(probe, "1");
      candidate.removeItem(probe);
      return candidate;
    } catch (_) {
      return null;
    }
  })();

  const getToken = () => storage ? storage.getItem(TOKEN_KEY) || "" : memoryToken;
  const getUser = () => storage ? storage.getItem(USER_KEY) || "" : memoryUser;
  const setSession = (token, user) => {
    memoryToken = token;
    memoryUser = user;
    if (storage) {
      storage.setItem(TOKEN_KEY, token);
      storage.setItem(USER_KEY, user);
    }
  };
  const clearSession = () => {
    memoryToken = "";
    memoryUser = "";
    if (storage) {
      storage.removeItem(TOKEN_KEY);
      storage.removeItem(USER_KEY);
    }
  };
  const authHeaders = (headers = {}) => {
    const result = new Headers(headers);
    const token = getToken();
    if (token) result.set("Authorization", `Bearer ${token}`);
    return result;
  };
  const emitChange = () => window.dispatchEvent(new CustomEvent("parabeacon-auth-changed"));
  const translate = (key, values = {}) => window.ParaBeaconI18n ? window.ParaBeaconI18n.t(key, values) : key;
  const setNotice = (element, message, error = false) => {
    element.textContent = message;
    element.className = `pb-auth-notice${error ? " error" : ""}`;
  };

  const addStyles = () => {
    if (document.getElementById("parabeacon-auth-styles")) return;
    const style = document.createElement("style");
    style.id = "parabeacon-auth-styles";
    style.textContent = `
      .pb-auth { display:inline-flex; align-items:center; gap:8px; position:relative; }
      .pb-auth-trigger, .pb-auth-account, .pb-auth-signout { border:1px solid var(--line, #2a3b57); border-radius:10px; padding:9px 12px; color:var(--text, #eef4ff); background:var(--panel-2, #18233b); font:inherit; font-size:13px; font-weight:800; cursor:pointer; }
      .pb-auth-trigger:hover, .pb-auth-account:hover, .pb-auth-signout:hover { filter:brightness(1.1); }
      .pb-auth-trigger { color:#06131a; border-color:transparent; background:var(--accent, #65e4bb); }
      .pb-auth-account { max-width:180px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
      .pb-auth-modal { position:fixed; z-index:1100; inset:0; display:grid; place-items:center; padding:20px; background:#030713aa; backdrop-filter:blur(8px); }
      .pb-auth-modal[hidden] { display:none; }
      .pb-auth-dialog { width:min(420px, 100%); padding:26px; border:1px solid var(--line, #2a3b57); border-radius:20px; color:var(--text, #eef4ff); background:linear-gradient(145deg, #17263fee, #0d1729f5); box-shadow:0 24px 80px #0009; }
      .pb-auth-dialog h2 { margin:0 0 7px; font-size:25px; letter-spacing:-.04em; }
      .pb-auth-dialog p { margin:0 0 20px; color:var(--muted, #91a0bd); font-size:13px; }
      .pb-auth-form { display:grid; gap:13px; }
      .pb-auth-form label { display:grid; gap:7px; color:var(--muted, #91a0bd); font-size:12px; font-weight:800; }
      .pb-auth-form input { width:100%; border:1px solid var(--line, #2a3b57); border-radius:10px; padding:11px 12px; color:var(--text, #eef4ff); background:#081326; font:inherit; outline:none; }
      .pb-auth-form input:focus { border-color:var(--accent-2, #8aa4ff); box-shadow:0 0 0 3px #8aa4ff22; }
      .pb-auth-form button[type=submit] { border:0; border-radius:10px; padding:11px 14px; color:#06131a; background:var(--accent, #65e4bb); font:inherit; font-weight:850; cursor:pointer; }
      .pb-auth-form button:disabled { cursor:wait; opacity:.6; }
      .pb-auth-close { float:right; border:1px solid var(--line, #2a3b57); border-radius:9px; padding:4px 9px; color:var(--text, #eef4ff); background:transparent; cursor:pointer; }
      .pb-auth-notice { min-height:19px; color:var(--muted, #91a0bd); font-size:12px; }
      .pb-auth-notice.error { color:var(--danger, #ff8e9f); }
      @media (max-width:700px) { .pb-auth-account { max-width:110px; } .pb-auth-signout { display:none; } }
    `;
    document.head.appendChild(style);
  };

  const init = (root) => {
    addStyles();
    root.replaceChildren();
    const trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "pb-auth-trigger";
    trigger.textContent = translate("authSignIn");
    const account = document.createElement("button");
    account.type = "button";
    account.className = "pb-auth-account";
    account.hidden = true;
    const signout = document.createElement("button");
    signout.type = "button";
    signout.className = "pb-auth-signout";
    signout.textContent = translate("authSignOut");
    signout.hidden = true;
    const modal = document.createElement("div");
    modal.className = "pb-auth-modal";
    modal.hidden = true;
    modal.innerHTML = `<section class="pb-auth-dialog" role="dialog" aria-modal="true" aria-labelledby="pb-auth-title"><button class="pb-auth-close" type="button" data-i18n-aria-label="authClose">×</button><h2 id="pb-auth-title" data-i18n="authTitle"></h2><p data-i18n="authDescription"></p><form class="pb-auth-form"><label><span data-i18n="authIdentifier"></span><input name="identifier" autocomplete="username" required></label><label><span data-i18n="authPassword"></span><input name="password" type="password" autocomplete="current-password" required></label><button type="submit" data-i18n="authSubmit"></button><div class="pb-auth-notice" role="status" aria-live="polite"></div></form></section>`;
    if (window.ParaBeaconI18n) window.ParaBeaconI18n.apply(modal);
    root.className = "pb-auth";
    root.append(trigger, account, signout, modal);
    const form = modal.querySelector("form");
    const close = () => { modal.hidden = true; };
    const open = () => { modal.hidden = false; modal.querySelector("input[name=identifier]").focus(); };
    const update = () => {
      const signedIn = Boolean(getToken());
      trigger.hidden = signedIn;
      account.hidden = !signedIn;
      signout.hidden = !signedIn;
      trigger.textContent = translate("authSignIn");
      account.textContent = getUser() || translate("authSignedIn");
      signout.textContent = translate("authSignOut");
      if (window.ParaBeaconI18n) window.ParaBeaconI18n.apply(modal);
    };
    trigger.addEventListener("click", open);
    account.addEventListener("click", open);
    signout.addEventListener("click", () => { clearSession(); update(); close(); emitChange(); });
    modal.addEventListener("click", (event) => { if (event.target === modal) close(); });
    modal.querySelector(".pb-auth-close").addEventListener("click", close);
    document.addEventListener("keydown", (event) => { if (event.key === "Escape") close(); });
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const submit = form.querySelector("button[type=submit]");
      const notice = form.querySelector(".pb-auth-notice");
      submit.disabled = true;
      setNotice(notice, translate("authSigningIn"));
      try {
        const response = await fetch("/api/v1/auth/login", { method:"POST", headers:{ "Content-Type":"application/json", "Accept":"application/json" }, body:JSON.stringify({ identifier:form.elements.identifier.value.trim(), password:form.elements.password.value }) });
        let body = {};
        try { body = await response.json(); } catch (_) {}
        if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
        if (!body.access_token) throw new Error(translate("authTokenMissing"));
        setSession(body.access_token, body.user && body.user.username ? body.user.username : "Signed in");
        form.reset();
        close();
        update();
        emitChange();
      } catch (error) {
        setNotice(notice, error.message, true);
      } finally {
        submit.disabled = false;
      }
    });
    update();
    window.addEventListener("parabeacon-language-changed", update);
  };

  window.ParaBeaconAuth = { getToken, authHeaders, init, logout: () => { clearSession(); emitChange(); } };
  document.addEventListener("DOMContentLoaded", () => document.querySelectorAll("[data-auth-widget]").forEach(init));
})();
