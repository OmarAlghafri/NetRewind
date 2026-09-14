import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Unmount between tests; Vitest does not enable testing-library's automatic
// cleanup without globals.
afterEach(() => cleanup());
