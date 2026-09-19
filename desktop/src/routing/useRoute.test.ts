import { describe, expect, it } from "vitest";
import { buildHash, parseRoute } from "./useRoute";

describe("parseRoute", () => {
  it("defaults to overview with no context for an empty hash", () => {
    expect(parseRoute("")).toEqual({ page: "overview", context: {} });
    expect(parseRoute("#/")).toEqual({ page: "overview", context: {} });
  });

  it("defaults to overview for an unrecognized page - never an error a user could see", () => {
    expect(parseRoute("#/not-a-real-page")).toEqual({ page: "overview", context: {} });
  });

  it("reads a plain page with no query string", () => {
    expect(parseRoute("#/incidents")).toEqual({ page: "incidents", context: {} });
  });

  it("reads scalar context fields", () => {
    expect(parseRoute("#/host?entity=10.0.0.5&query=eth0")).toEqual({
      page: "host",
      context: { entity: "10.0.0.5", query: "eth0" },
    });
  });

  it("reads comma-separated array fields (severities, families) as arrays", () => {
    expect(parseRoute("#/timeline?severities=warn,error&families=l2,l3")).toEqual({
      page: "timeline",
      context: { severities: ["warn", "error"], families: ["l2", "l3"] },
    });
  });

  it("drops empty values instead of keeping empty strings/arrays", () => {
    expect(parseRoute("#/incidents?entity=&severities=")).toEqual({ page: "incidents", context: {} });
  });
});

describe("buildHash", () => {
  it("round-trips through parseRoute for every field, scalar and array", () => {
    const route = {
      page: "timeline" as const,
      context: {
        from: "2026-09-19T10:00:00Z",
        to: "2026-09-19T11:00:00Z",
        entity: "10.0.0.5",
        severities: ["warn", "error"],
        families: ["l2", "l3"],
        query: "eth0",
        sort: "desc",
        selection: "01ABC",
      },
    };
    expect(parseRoute(buildHash(route))).toEqual(route);
  });

  it("produces a bare page hash with no trailing '?' when context is empty", () => {
    expect(buildHash({ page: "overview", context: {} })).toBe("#/overview");
  });
});
