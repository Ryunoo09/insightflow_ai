import { describe, expect, it } from "vitest";

import { buildDatasetListQuery, buildRowsQuery } from "./api";

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

describe("buildRowsQuery", () => {
  it("keeps only the required fields when nothing else is set", () => {
    expect(buildRowsQuery({ offset: 0, limit: 100 })).toEqual({ offset: 0, limit: 100 });
  });

  it("maps version, sort, and trimmed search", () => {
    expect(
      buildRowsQuery({
        offset: 100,
        limit: 100,
        versionNo: 2,
        sort: [{ column: "revenue", direction: "desc" }],
        search: " jakarta ",
      }),
    ).toEqual({
      offset: 100,
      limit: 100,
      version_no: 2,
      sort: [{ column: "revenue", direction: "desc" }],
      search: "jakarta",
    });
  });
});
