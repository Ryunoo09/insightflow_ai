import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";

import { Badge, Card } from "../../../components/ui";
import { ApiError } from "../../../lib/api";
import { workspacesApi } from "../api";

/** Initial dashboard for the workspace in the URL (/app/:workspaceId/overview). */
export function OverviewPage() {
  const { workspaceId = "" } = useParams();
  const { data, isPending, error } = useQuery({
    queryKey: ["workspace", workspaceId],
    queryFn: () => workspacesApi.get(workspaceId),
  });

  if (isPending) {
    return <p className="text-sm text-muted-foreground">Memuat...</p>;
  }

  if (error) {
    const notFound = error instanceof ApiError && error.status === 404;
    return (
      <Card className="space-y-2">
        <h1 className="text-xl font-semibold">Ruang kerja tidak tersedia</h1>
        <p className="text-sm text-muted-foreground">
          {notFound ? "Ruang kerja tidak ditemukan atau kamu tidak punya akses." : "Gagal memuat ruang kerja."}
        </p>
        <Link to="/app" className="text-sm font-medium text-primary hover:underline">
          Pilih ruang kerja lain
        </Link>
      </Card>
    );
  }

  return (
    <div className="space-y-6">
      <header className="flex items-center gap-3">
        <h1 className="text-2xl font-semibold">{data.name}</h1>
        <Badge>{data.role}</Badge>
        <span className="text-sm text-muted-foreground">/{data.slug}</span>
      </header>

      <Card>
        <h2 className="text-lg font-semibold">Mulai analisis</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          Unggah dataset CSV atau XLSX untuk mulai menganalisis data di ruang kerja ini.
        </p>
        <Link
          to={`/app/${workspaceId}/datasets`}
          className="mt-3 inline-block text-sm font-medium text-primary hover:underline"
        >
          Kelola dataset
        </Link>
      </Card>
    </div>
  );
}
