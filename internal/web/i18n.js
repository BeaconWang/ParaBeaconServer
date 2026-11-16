(() => {
  const translations = {
    en: {
      language: "中文",
      languageLabel: "Switch language",
      homeTitle: "ParaBeacon",
      homeEyebrow: "A clearer view of every flight",
      homeHeroTitle: "Fly informed. Stay connected.",
      homeHeroCopy: "ParaBeacon brings public live tracking and pilot-ready Android releases into one focused operations hub.",
      homeSecureNote: "Secure access · privacy-first sharing · built for pilots",
      homeChoose: "Choose a starting point",
      homeChooseCopy: "Use the tools you need now, then return here whenever you need the wider picture.",
      homeLiveTitle: "Live tracking map",
      homeLiveCopy: "See every currently shared pilot on a world map without signing in. Inspect position, heading, freshness, and last observation time.",
      homeLiveAction: "Open live map",
      homeReleaseTitle: "Release console",
      homeReleaseCopy: "Publish signed Android builds, review release history, inspect checksums, and keep the latest package ready for pilots.",
      homeReleaseAction: "Open release console",
      homeWatch: "WATCH",
      homeWatchTitle: "Open the live map",
      homeWatchCopy: "View opt-in public locations immediately; sign in only when you need account access.",
      homePrepare: "PREPARE",
      homePrepareTitle: "Choose a release",
      homePrepareCopy: "Use the release console to keep your Android client on a verified build.",
      homeFly: "FLY",
      homeFlyTitle: "Share intentionally",
      homeFlyCopy: "Public sharing is opt-in and can be revoked by the pilot at any time.",
      homeFooter: "ParaBeacon · Flight operations hub",
      status: "System status",
      brandCaption: "Flight operations",
      navPrimary: "Primary navigation",
      paraBeaconHome: "ParaBeacon home",
      livePageTitle: "ParaBeacon Live Tracking",
      liveEyebrow: "ParaBeacon / Live",
      liveHeading: "Public flight tracking",
      liveHome: "Home",
      liveRelease: "Release console",
      livePilots: "Live pilots",
      liveLoading: "Loading public locations",
      liveConnecting: "Connecting",
      liveLoadingPilots: "Loading pilots who opted into public live tracking…",
      liveFooter: "Only pilots who enabled public sharing appear here. Positions are refreshed every 15 seconds.",
      liveEmpty: "No public live pilots were found in the latest update.",
      liveUnavailable: "Unavailable",
      liveMapUnavailable: "Map unavailable",
      liveUpdated: "Updated {{time}} · {{count}} public pilot{{plural}}",
      liveSingular: "",
      livePlural: "s",
      liveRefreshFailed: "The map could not be refreshed",
      liveHeadingValue: "Heading {{value}}°",
      liveHeadingMissing: "Heading not reported",
      liveNow: "LIVE",
      liveStale: "STALE",
      liveUnknownPilot: "Unknown pilot",
      liveLatitude: "Latitude",
      liveLongitude: "Longitude",
      liveHeadingLabel: "Heading",
      liveNotReported: "Not reported",
      liveObserved: "Observed",
      authSignIn: "Sign in",
      authSignOut: "Sign out",
      authSignedIn: "Signed in",
      authTitle: "Sign in to ParaBeacon",
      authDescription: "Your account lets protected tools recognize you across this browser tab. The live map remains publicly viewable without signing in.",
      authClose: "Close sign-in dialog",
      authIdentifier: "Username or email",
      authPassword: "Password",
      authSubmit: "Sign in",
      authSigningIn: "Signing in…",
      authTokenMissing: "The server did not return an access token",
      requestFailed: "Request failed ({{status}})"
    },
    zh: {
      language: "English",
      languageLabel: "切换语言",
      homeTitle: "ParaBeacon",
      homeEyebrow: "清晰掌握每一次飞行",
      homeHeroTitle: "看得更清楚，飞得更从容。",
      homeHeroCopy: "ParaBeacon 将公开实时追踪与面向飞行员的 Android 版本管理，整合到一个专注的运行中心。",
      homeSecureNote: "安全访问 · 隐私优先分享 · 为飞行员打造",
      homeChoose: "选择开始位置",
      homeChooseCopy: "从现在需要的工具开始，也可以随时回到这里查看全局。",
      homeLiveTitle: "实时追踪地图",
      homeLiveCopy: "无需登录即可在世界地图上查看当前公开分享的飞行员，并了解位置、航向、更新时间和新鲜度。",
      homeLiveAction: "打开实时地图",
      homeReleaseTitle: "版本发布控制台",
      homeReleaseCopy: "发布已签名的 Android 构建，查看版本历史、校验摘要，并为飞行员准备最新安装包。",
      homeReleaseAction: "打开发布控制台",
      homeWatch: "观测",
      homeWatchTitle: "打开实时地图",
      homeWatchCopy: "立即查看主动公开的位置；只有需要账户功能时才登录。",
      homePrepare: "准备",
      homePrepareTitle: "选择版本",
      homePrepareCopy: "使用发布控制台，让 Android 客户端保持在已验证的版本上。",
      homeFly: "飞行",
      homeFlyTitle: "有意识地分享",
      homeFlyCopy: "公开分享完全自愿，飞行员可以随时撤销。",
      homeFooter: "ParaBeacon · 飞行运行中心",
      status: "系统状态",
      brandCaption: "飞行运行",
      navPrimary: "主导航",
      paraBeaconHome: "ParaBeacon 首页",
      livePageTitle: "ParaBeacon 实时追踪",
      liveEyebrow: "ParaBeacon / 实时",
      liveHeading: "公开飞行追踪",
      liveHome: "首页",
      liveRelease: "发布控制台",
      livePilots: "实时飞行员",
      liveLoading: "正在加载公开位置",
      liveConnecting: "正在连接",
      liveLoadingPilots: "正在加载已选择公开分享的飞行员…",
      liveFooter: "这里只显示已启用公开分享的飞行员。位置每 15 秒刷新一次。",
      liveEmpty: "最近一次更新中没有找到公开的实时飞行员。",
      liveUnavailable: "暂不可用",
      liveMapUnavailable: "地图不可用",
      liveUpdated: "更新于 {{time}} · {{count}} 位公开飞行员",
      liveSingular: "",
      livePlural: "",
      liveRefreshFailed: "地图刷新失败",
      liveHeadingValue: "航向 {{value}}°",
      liveHeadingMissing: "未报告航向",
      liveNow: "实时",
      liveStale: "过期",
      liveUnknownPilot: "未知飞行员",
      liveLatitude: "纬度",
      liveLongitude: "经度",
      liveHeadingLabel: "航向",
      liveNotReported: "未报告",
      liveObserved: "观测时间",
      authSignIn: "登录",
      authSignOut: "退出登录",
      authSignedIn: "已登录",
      authTitle: "登录 ParaBeacon",
      authDescription: "登录后，受保护的工具可以在当前浏览器标签页中识别你的账户。实时地图无需登录即可查看。",
      authClose: "关闭登录窗口",
      authIdentifier: "用户名或邮箱",
      authPassword: "密码",
      authSubmit: "登录",
      authSigningIn: "正在登录…",
      authTokenMissing: "服务器没有返回访问令牌",
      requestFailed: "请求失败（{{status}}）"
    }
  };

  const browserLanguage = () => ((navigator.language || "").toLowerCase().startsWith("zh") ? "zh" : "en");
  const stored = (() => {
    try { return window.localStorage.getItem("parabeacon-language"); } catch (_) { return null; }
  })();
  let language = stored === "zh" || stored === "en" ? stored : browserLanguage();
  const t = (key, values = {}) => Object.entries(values).reduce((text, [name, value]) => text.split(`{{${name}}}`).join(String(value)), (translations[language] || translations.en)[key] || key);
  const apply = (root = document) => {
    document.documentElement.lang = language;
    if (root === document && document.body.dataset.pageTitle) document.title = t(document.body.dataset.pageTitle);
    root.querySelectorAll("[data-i18n]").forEach((element) => { element.textContent = t(element.dataset.i18n); });
    root.querySelectorAll("[data-i18n-placeholder]").forEach((element) => { element.placeholder = t(element.dataset.i18nPlaceholder); });
    root.querySelectorAll("[data-i18n-aria-label]").forEach((element) => { element.setAttribute("aria-label", t(element.dataset.i18nAriaLabel)); });
    root.querySelectorAll("[data-i18n-title]").forEach((element) => { element.title = t(element.dataset.i18nTitle); });
    root.querySelectorAll("[data-language-toggle]").forEach((element) => { element.textContent = t("language"); element.setAttribute("aria-label", t("languageLabel")); });
  };
  const setLanguage = (next, shouldApply = true) => {
    language = next === "zh" ? "zh" : "en";
    try { window.localStorage.setItem("parabeacon-language", language); } catch (_) {}
    if (shouldApply) apply();
    window.dispatchEvent(new CustomEvent("parabeacon-language-changed", { detail: { language } }));
  };
  const toggle = () => setLanguage(language === "en" ? "zh" : "en");
  const init = () => {
    if (document.body.dataset.i18nShared !== "true") return;
    apply();
    document.querySelectorAll("[data-language-toggle]").forEach((element) => element.addEventListener("click", toggle));
  };
  window.ParaBeaconI18n = { t, apply, init, toggle, setLanguage, getLanguage: () => language };
  document.addEventListener("DOMContentLoaded", init);
})();
