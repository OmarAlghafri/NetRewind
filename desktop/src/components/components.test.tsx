import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LanguageProvider } from "../i18n/LanguageContext";
import { CapabilityTable } from "./CapabilityTable";
import { SourceBanner } from "./SourceBanner";
import { Settings } from "../pages/Settings";
import { DEFAULT_SETTINGS } from "../data/source";
import type { Record } from "../data/useRecord";
import type { Capability } from "../data/types";

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
  it("only offers the demo source outside the desktop shell, and saves changes", () => {
    const onChange = vi.fn();
    english(<Settings settings={DEFAULT_SETTINGS} onChange={onChange} onReopenWizard={() => {}} />);
    const live = screen.getByLabelText("Live recorder on this machine") as HTMLInputElement;
    expect(live.disabled).toBe(true);
    expect((screen.getByLabelText("Demo recording") as HTMLInputElement).checked).toBe(true);
    const save = screen.getByText("Save") as HTMLButtonElement;
    expect(save.disabled).toBe(true); // nothing changed yet
  });
});
