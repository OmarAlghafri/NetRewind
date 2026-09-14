// Mirrors the localStorage pattern in ../i18n/LanguageContext.tsx exactly,
// including the try/catch guard for a locked-down webview where
// localStorage can throw rather than simply being empty.

const STORAGE_KEY = "netrewind.onboardingComplete";

export function readOnboardingComplete(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === "1";
  } catch {
    // localStorage can throw in a locked-down webview; default below covers it.
  }
  return false;
}

export function writeOnboardingComplete(done: boolean): void {
  try {
    if (done) {
      window.localStorage.setItem(STORAGE_KEY, "1");
    } else {
      window.localStorage.removeItem(STORAGE_KEY);
    }
  } catch {
    // Not fatal: the choice just does not survive a restart this session.
  }
}
