import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/repos/$repo")({
  component: RepoPage,
});

function RepoPage() {
  return <h1>Repository</h1>;
}
