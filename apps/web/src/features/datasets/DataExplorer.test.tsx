import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "../../lib/api";
import { DataExplorer } from "./DataExplorer";

// The grid itself is heavy and needs layout; capture its props instead and
// exercise the datasource contract our code owns.
const grid = vi.hoisted(() => ({ props: null as Record<string, unknown> | null }));
const rows = vi.hoisted(() => vi.fn());

vi.mock("ag-grid-react", () => ({
  AgGridReact: (props: Record<string, unknown>) => {
    grid.props = props;
    return null;
  },
}));

vi.mock("./api", () => ({ datasetsApi: { rows } }));

type Datasource = {
  getRows: (params: {
    startRow: number;
    endRow: number;
    sortModel: { colId: string; sort: string }[];
    successCallback: (data: unknown[], total: number) => void;
    failCallback: () => void;
  }) => void;
};

function renderExplorer() {
  render(
    <DataExplorer
      datasetId="ds-1"
      columns={[{ name: "id", type: "number" }]}
      version={0}
      search=""
      initialSort={null}
      onSortChange={() => {}}
    />,
  );
  return (grid.props as { datasource: Datasource }).datasource;
}

describe("DataExplorer datasource", () => {
  beforeEach(() => {
    rows.mockReset();
  });

  it("loads a 100-row block and reports the total", async () => {
    const block = Array.from({ length: 100 }, (_, index) => ({ id: index }));
    rows.mockResolvedValue({ columns: [{ name: "id", type: "number" }], rows: block, total: 250 });

    const datasource = renderExplorer();
    const success = vi.fn();
    await datasource.getRows({ startRow: 0, endRow: 100, sortModel: [], successCallback: success, failCallback: vi.fn() });

    expect(rows).toHaveBeenCalledWith("ds-1", expect.objectContaining({ offset: 0, limit: 100 }));
    expect(success).toHaveBeenCalledWith(block, 250);
  });

  it("forwards the grid sort to the server", async () => {
    rows.mockResolvedValue({ columns: [{ name: "id", type: "number" }], rows: [], total: 0 });

    const datasource = renderExplorer();
    await datasource.getRows({
      startRow: 200,
      endRow: 300,
      sortModel: [{ colId: "id", sort: "desc" }],
      successCallback: vi.fn(),
      failCallback: vi.fn(),
    });

    expect(rows).toHaveBeenCalledWith(
      "ds-1",
      expect.objectContaining({ offset: 200, limit: 100, sort: [{ column: "id", direction: "desc" }] }),
    );
  });

  it("fails the block when the query errors", async () => {
    rows.mockRejectedValue(new ApiError(503, "ENGINE_UNAVAILABLE", "down"));

    const datasource = renderExplorer();
    const fail = vi.fn();
    await datasource.getRows({ startRow: 0, endRow: 100, sortModel: [], successCallback: vi.fn(), failCallback: fail });
    await Promise.resolve();

    expect(fail).toHaveBeenCalled();
  });
});