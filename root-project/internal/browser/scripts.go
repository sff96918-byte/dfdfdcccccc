package browser

const storageFunction = `() => {
  const copy = (storage) => {
    const data = {};
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      data[key] = storage.getItem(key);
    }
    return data;
  };

  return {
    local_storage: copy(window.localStorage),
    session_storage: copy(window.sessionStorage),
  };
}`

const fingerprintFunction = `() => ({
  userAgent: navigator.userAgent,
  language: navigator.language,
  colorDepth: String(screen.colorDepth),
  screenResolution: String(screen.width) + "x" + String(screen.height),
  timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  addBehavior: String("addBehavior" in document.documentElement),
  openDatabase: String("openDatabase" in window),
  plugins: Array.from(navigator.plugins).map((plugin) => plugin.name).join(","),
  canvas: (() => {
    const canvas = document.createElement("canvas");
    const context = canvas.getContext("2d");
    if (context) {
      context.fillText("BF", 2, 2);
    }
    return canvas.toDataURL().slice(-50);
  })(),
  webgl: (() => {
    const context = document.createElement("canvas").getContext("webgl");
    if (!context) {
      return "~";
    }
    return String(context.getParameter(context.VENDOR)) + "~" + String(context.getParameter(context.RENDERER));
  })(),
  touchSupport: String("ontouchstart" in window),
  hardwareConcurrency: String(navigator.hardwareConcurrency || "unknown"),
})`

const fetchFunction = `async (request) => {
  const options = {
    method: request.method,
    credentials: "include",
    headers: request.headers || {},
  };

  if (Object.prototype.hasOwnProperty.call(request, "body")) {
    options.body = request.body;
  }

  try {
    const response = await fetch(request.url, options);
    const text = await response.text();
    return {
      ok: response.ok,
      status: response.status,
      statusText: response.statusText,
      text,
    };
  } catch (error) {
    return {
      ok: false,
      status: 0,
      statusText: "FETCH_ERROR",
      text: String(error),
    };
  }
}`
