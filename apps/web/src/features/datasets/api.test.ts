import { describe, expect, it } from "vitest";

import { buildDatasetListQuery } from "./api";

describe("buildDatasetListQuery", () => {
  it("maps params to the server contract keys", () => {
    const qs = buildDatasetListQuery({ page: 2, perPage: 20, q: " sales ", status: "ready", sort: "name", order: "asc" });
    expect(qs).toBe("page=2&per_page=20&q=sales&status=ready&sort=name&order=asc");
  });

  it("omits empty params", () => {
    expect(buildDatasetListQuery({})).toBe("");
    expect(buildDatasetListQuery({ q: "   ", status: "" })).toBe("");
  });

  it("drops a sort value outside the allowlist", () => {
    expect(buildDatasetListQuery({ sort: "drop table" })).toBe("");
  });
});
