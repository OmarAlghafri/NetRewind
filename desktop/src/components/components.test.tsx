import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { CapabilityTable } from "./CapabilityTable";
import { SourceBanner } from "./SourceBanner";
import { Settings } from "../pages/Settings";
import { DEFAULT_SETTINGS } from "../data/source";
import { DEFAULT_AI_SETTINGS } from "../data/aiSettings";
import type { Record } from "../data/useRecord";
import type { Capability } from "../data/types";

// The "Settings: local AI assistant card" describe block below exercises
// what the card renders once past the compile-time feature gate - a
// companion, unmocked test proves the card is hidden entirely against the
// real flag in Settings.featureGate.test.tsx.
vi.mock("../data/aiFeature", () => ({ AI_FEATURE_ENABLED: true }));

function english<T>(ui: React.ReactElement<T>) {
  window.localStorage.setItem("netrewind.lang", "en");
  return render(<LanguageProvider>{ui}</LanguageProvider>);
}

const record = (over: Partial<Record>): Record => ({
  status: "ready", error: "", events: [], incidents: [], health: null, capabilities: [], rules: [],
  manifest: null, bundleSigned: false, bundleHasSignature: false, refreshedAt: null, stale: false,
  refresh: () => {}, ...over,
});

describe("CapabilityTable", () => {
  it("shows up, down with its reason, and unsupported as distinct states", () => {
    const caps: Capability[] = [
      { name: "iphelper.link", platform: "windows", privilege: "none", coverage: ["link.*"], status: "up", last_change: "", last_seen: "" },
      { name: "policy.nftables", platform: "linux", privilege: "CAP_NET_ADMIN", coverage: ["policy.rule_changed"], status: "down", reason: "nft: executable file not found", last_change: "", last_seen: "" },
      { name: "ebpf.flow", platform: "linux", privilege: "CAP_BPF", coverage: ["flow.*"], status: "unsupported", reason: "requires linux", last_change: "", last_seen: "" },
    ];
    english(<CapabilityTable capabilities={caps} />);
    expect(screen.getByText("iphelper.link")).toBeInTheDocument();
    expect(screen.getByText("watching")).toBeInTheDocument();
    expect(screen.getByText(/nft: executable file not found/)).toBeInTheDocument();
    expect(screen.getByText("not supported on this platform")).toBeInTheDocument();
    // an unsupported collector's reason is the platform, not shown as a failure
    expect(screen.queryByText(/requires linux/)).not.toBeInTheDocument();
    // what is watching is listed before what is down, before what cannot run here
    const names = screen.getAllByText(/^(iphelper\.link|policy\.nftables|ebpf\.flow)$/).map((el) => el.textContent);
    expect(names).toEqual(["iphelper.link", "policy.nftables", "ebpf.flow"]);
  });

  it("translates a coded down reason and keeps the raw text available, collapsed", () => {
    const caps: Capability[] = [
      {
        name: "netlink.link",
        platform: "linux",
        privilege: "none",
        coverage: ["link.*"],
        status: "down",
        reason: "stopped",
        reason_code: "collector_stopped",
        reason_params: {},
        last_change: "",
        last_seen: "",
      },
    ];
    english(<CapabilityTable capabilities={caps} />);
    expect(screen.getByText(/This collector stopped\./)).toBeInTheDocument();
    // The raw reason is still there, just inside a collapsed <details> -
    // "available on demand" (ADR 0004 §4.5), not gone.
    const detail = screen.getByText("Technical detail").closest("details");
    expect(detail).not.toHaveAttribute("open");
    expect(detail).toHaveTextContent("stopped");
  });
});

describe("SourceBanner", () => {
  it("shows the recorder's error with a retry that calls refresh", () => {
    const refresh = vi.fn();
    english(
      <SourceBanner
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        record={record({ status: "error", error: "no recorder is listening", refresh })}
        onSwitchToDemo={() => {}}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("no recorder is listening");
    fireEvent.click(screen.getByText("Retry"));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("explains a shell-only source in the browser and offers the demo", () => {
    const toDemo = vi.fn();
    english(
      <SourceBanner
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        record={record({ status: "error", error: "shell_required" })}
        onSwitchToDemo={toDemo}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("needs the desktop application");
    fireEvent.click(screen.getByText("Switch to demo"));
    expect(toDemo).toHaveBeenCalledTimes(1);
  });

  it("translates a structured connection error and keeps the raw detail available, collapsed", () => {
    english(
      <SourceBanner
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        record={record({
          status: "error",
          error: {
            code: "not_listening",
            params: { endpoint: "/run/netrewind/api.sock" },
            technical_detail: "no recorder is listening at /run/netrewind/api.sock (is the netrewindd service running?)",
          },
        })}
        onSwitchToDemo={() => {}}
      />,
    );
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("No recorder is listening at /run/netrewind/api.sock");
    // The raw English sentence is still there, just inside <details> -
    // "available on demand" (ADR 0004 §4.5), not gone and not shown
    // unconditionally alongside the translated sentence.
    expect(screen.getByText("Technical detail").closest("details")).not.toHaveAttribute("open");
    expect(alert).toHaveTextContent("is the netrewindd service running?");
  });

  it("falls back to technical_detail for a code this build does not recognise, with no redundant detail toggle", () => {
    english(
      <SourceBanner
        settings={{ ...DEFAULT_SETTINGS, kind: "live" }}
        record={record({
          status: "error",
          error: { code: "some_future_code", params: {}, technical_detail: "a brand new failure mode" },
        })}
        onSwitchToDemo={() => {}}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("a brand new failure mode");
    // The message already is technical_detail here - a second copy behind
    // "Technical detail" would just repeat it.
    expect(screen.queryByText("Technical detail")).not.toBeInTheDocument();
  });

  it("labels a live source as connected when ready", () => {
    english(
      <SourceBanner settings={{ ...DEFAULT_SETTINGS, kind: "live" }} record={record({ refreshedAt: new Date() })} onSwitchToDemo={() => {}} />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Connected to the live recorder");
  });
});

describe("Settings", () => {
  it("only offers the demo source outside the desktop shell", () => {
    english(<Settings settings={DEFAULT_SETTINGS} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);
    const live = screen.getByLabelText("Live recorder on this machine") as HTMLInputElement;
    expect(live.disabled).toBe(true);
    expect((screen.getByLabelText("Demo recording") as HTMLInputElement).checked).toBe(true);
  });

  // execution order §9 Phase 5: "a sticky save/discard bar" - shown only
  // once there is something to decide about, not a permanently-visible,
  // merely-disabled button.
  it("shows no save/discard bar at all until something actually changes", () => {
    english(<Settings settings={DEFAULT_SETTINGS} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);
    expect(screen.queryByText("Save")).not.toBeInTheDocument();
    expect(screen.queryByText("Discard changes")).not.toBeInTheDocument();
  });

  // Outside the shell (this test environment), the live/bundle radios are
  // disabled, so "demo" is the only kind a click can actually change -
  // starting from a saved setting of "live" (a real prior state, e.g. from
  // the desktop app) lets clicking the always-enabled "Demo recording"
  // radio genuinely dirty the form without needing to fake being in the
  // shell just to exercise the save bar.
  const savedAsLive = { ...DEFAULT_SETTINGS, kind: "live" as const };

  it("shows the save bar once a field changes, and saves the new value", () => {
    const onChange = vi.fn();
    english(<Settings settings={savedAsLive} onChange={onChange} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);
    expect(screen.queryByText("You have unsaved changes")).not.toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Demo recording"));
    expect(screen.getByText("You have unsaved changes")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Save"));
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange.mock.calls[0][0]).toMatchObject({ kind: "demo" });
  });

  it("discarding reverts the draft without calling onChange", () => {
    const onChange = vi.fn();
    english(<Settings settings={savedAsLive} onChange={onChange} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);

    fireEvent.click(screen.getByLabelText("Demo recording"));
    expect(screen.getByText("You have unsaved changes")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Discard changes"));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.queryByText("You have unsaved changes")).not.toBeInTheDocument();
    // Reverted to the saved value (live), not left on the discarded draft.
    expect((screen.getByLabelText("Demo recording") as HTMLInputElement).checked).toBe(false);
  });
});

describe("Settings: local AI assistant card", () => {
  it("hides the profile/model-file/threads fields until the assistant is enabled", () => {
    english(<Settings settings={DEFAULT_SETTINGS} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);
    expect(screen.queryByLabelText("Downloaded model file name")).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("Turn on the local AI assistant"));
    expect(screen.getByLabelText("Downloaded model file name")).toBeInTheDocument();
  });

  it("one dirty check and one save bar covers both source and AI settings - changing only the AI side still shows it", () => {
    english(<Settings settings={DEFAULT_SETTINGS} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);
    expect(screen.queryByText("You have unsaved changes")).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("Turn on the local AI assistant"));
    expect(screen.getByText("You have unsaved changes")).toBeInTheDocument();
  });

  it("saving calls onChangeAiSettings with the edited draft, leaving the source settings call intact", () => {
    const onChange = vi.fn();
    const onChangeAiSettings = vi.fn();
    english(<Settings settings={DEFAULT_SETTINGS} onChange={onChange} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={onChangeAiSettings} onReopenWizard={() => {}} />);

    fireEvent.click(screen.getByLabelText("Turn on the local AI assistant"));
    fireEvent.change(screen.getByLabelText("Downloaded model file name"), { target: { value: "small.gguf" } });
    fireEvent.click(screen.getByText("Save"));

    expect(onChangeAiSettings).toHaveBeenCalledTimes(1);
    expect(onChangeAiSettings.mock.calls[0][0]).toMatchObject({ enabled: true, modelFileName: "small.gguf" });
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange.mock.calls[0][0]).toEqual(DEFAULT_SETTINGS);
  });

  it("discarding reverts the AI draft too, without calling onChangeAiSettings", () => {
    const onChangeAiSettings = vi.fn();
    english(<Settings settings={DEFAULT_SETTINGS} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={onChangeAiSettings} onReopenWizard={() => {}} />);

    fireEvent.click(screen.getByLabelText("Turn on the local AI assistant"));
    fireEvent.click(screen.getByText("Discard changes"));

    expect(onChangeAiSettings).not.toHaveBeenCalled();
    expect((screen.getByLabelText("Turn on the local AI assistant") as HTMLInputElement).checked).toBe(false);
  });
});

// execution order §9 Phase 5: "an unsaved-changes guard". In-app hash
// navigation never fires `beforeunload` at all (the document never
// unloads), so it needs its own guard - proven directly by dispatching a
// real `hashchange` event and checking whether the hash actually reverts,
// not by inspecting internal state.
describe("Settings unsaved-changes guard", () => {
  afterEach(() => {
    window.location.hash = "";
  });

  it("reverts an in-app navigation away from a dirty form when the user declines to lose it", () => {
    window.location.hash = "#/settings";
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    english(<Settings settings={{ ...DEFAULT_SETTINGS, kind: "live" }} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);
    fireEvent.click(screen.getByLabelText("Demo recording"));
    expect(screen.getByText("You have unsaved changes")).toBeInTheDocument();

    window.location.hash = "#/incidents";
    window.dispatchEvent(new HashChangeEvent("hashchange"));

    expect(confirmSpy).toHaveBeenCalledTimes(1);
    expect(window.location.hash).toBe("#/settings");
    confirmSpy.mockRestore();
  });

  it("lets the navigation through when the user confirms losing the draft", () => {
    window.location.hash = "#/settings";
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);
    english(<Settings settings={{ ...DEFAULT_SETTINGS, kind: "live" }} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);
    fireEvent.click(screen.getByLabelText("Demo recording"));

    window.location.hash = "#/incidents";
    window.dispatchEvent(new HashChangeEvent("hashchange"));

    expect(confirmSpy).toHaveBeenCalledTimes(1);
    expect(window.location.hash).toBe("#/incidents");
    confirmSpy.mockRestore();
  });

  it("does not ask at all when the form is clean", () => {
    window.location.hash = "#/settings";
    const confirmSpy = vi.spyOn(window, "confirm");
    english(<Settings settings={DEFAULT_SETTINGS} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);

    window.location.hash = "#/incidents";
    window.dispatchEvent(new HashChangeEvent("hashchange"));

    expect(confirmSpy).not.toHaveBeenCalled();
    expect(window.location.hash).toBe("#/incidents");
    confirmSpy.mockRestore();
  });

  it("marks a beforeunload event as needing confirmation while the form is dirty, and not when it is clean", () => {
    english(<Settings settings={{ ...DEFAULT_SETTINGS, kind: "live" }} onChange={() => {}} aiSettings={DEFAULT_AI_SETTINGS} onChangeAiSettings={() => {}} onReopenWizard={() => {}} />);

    const cleanEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(cleanEvent);
    expect(cleanEvent.defaultPrevented).toBe(false);

    fireEvent.click(screen.getByLabelText("Demo recording"));
    const dirtyEvent = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(dirtyEvent);
    expect(dirtyEvent.defaultPrevented).toBe(true);
  });
});
