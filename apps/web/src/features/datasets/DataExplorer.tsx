import { useEffect, useMemo, useRef, useState } from "react";
import { AgGridReact } from "ag-grid-react";
import type { ColDef, GridApi, GridReadyEvent, IDatasource, SortChangedEvent } from "ag-grid-community";
import "ag-grid-community/styles/ag-grid.css";
import "ag-grid-community/styles/ag-theme-quartz.css";

import { ApiError } from "../../lib/api";
import { datasetsApi, type ExplorerColumn, type ExplorerSort } from "./api";

const BLOCK_SIZE = 100;

// Column type indicators in the header (Plan 05 section 5).
const TYPE_GLYPH: Record<string, string> = { number: "#", text: "Aa", date: "📅", boolean: "◑" };
const TYPE_LABEL: Record<string, string> = { number: "angka", text: "teks", date: "tanggal", boolean: "boolean" };

type Props = {
  datasetId: string;
  columns: ExplorerColumn[];
  version: number;
  search: string;
  initialSort: ExplorerSort | null;
  onSortChange: (sort: ExplorerSort | null) => void;
};

/** Virtualized, server-paginated dataset grid (AG Grid Infinite Row Model). */
export function DataExplorer({ datasetId, columns, version, search, initialSort, onSortChange }: Props) {
  const apiRef = useRef<GridApi | null>(null);
  const [error, setError] = useState<string | null>(null);

  const columnDefs = useMemo<ColDef[]>(
    () =>
      columns.map((column) => {
        const label = column.original_name || column.name;
        return {
          field: column.name,
          headerName: `${TYPE_GLYPH[column.type] ?? "Aa"} ${label}`,
          headerTooltip: `${label} (${TYPE_LABEL[column.type] ?? column.type})`,
          minWidth: 120,
          sortable: true,
          filter: false,
          cellClass: column.type === "number" ? "text-right tabular-nums" : column.type === "date" ? "whitespace-nowrap" : undefined,
          headerClass: column.type === "number" ? "text-right" : undefined,
        };
      }),
    [columns],
  );

  const datasource = useMemo<IDatasource>(
    () => ({
      getRows: (params) => {
        const limit = params.endRow - params.startRow;
        const sort = params.sortModel
          .filter((model) => model.colId)
          .map((model) => ({ column: model.colId, direction: model.sort === "asc" ? "asc" : "desc" })) as ExplorerSort[];
        datasetsApi
          .rows(datasetId, { offset: params.startRow, limit, versionNo: version || undefined, sort, search })
          .then((page) => {
            setError(null);
            params.successCallback(page.rows, page.total);
          })
          .catch((err) => {
            setError(err instanceof ApiError ? err.message : "Gagal memuat baris data.");
            params.failCallback();
          });
      },
    }),
    [datasetId, version, search],
  );

  // A new datasource (quick filter or version change) must reload every block.
  useEffect(() => {
    apiRef.current?.setGridOption("datasource", datasource);
  }, [datasource]);

  const initialState = useMemo(
    () => ({
      sort: {
        sortModel: initialSort ? [{ colId: initialSort.column, sort: initialSort.direction }] : [],
      },
    }),
    // Applied once on mount; later sort changes come from the grid itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  function onGridReady(event: GridReadyEvent) {
    apiRef.current = event.api;
  }

  function onSortChanged(event: SortChangedEvent) {
    const active = event.api.getColumnState().find((column) => column.sort);
    onSortChange(active?.colId ? { column: active.colId, direction: active.sort === "asc" ? "asc" : "desc" } : null);
  }

  return (
    <div className="space-y-2">
      {error && <p className="text-sm text-danger">{error}</p>}
      <div className="ag-theme-quartz w-full" style={{ height: "calc(100vh - 260px)", minHeight: 420 }}>
        <AgGridReact
          rowModelType="infinite"
          cacheBlockSize={BLOCK_SIZE}
          maxBlocksInCache={10}
          infiniteInitialRowCount={BLOCK_SIZE}
          columnDefs={columnDefs}
          defaultColDef={{ resizable: true, sortable: true }}
          datasource={datasource}
          initialState={initialState}
          onGridReady={onGridReady}
          onSortChanged={onSortChanged}
          animateRows={false}
        />
      </div>
    </div>
  );
}