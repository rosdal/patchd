import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/findings/$id")({
  component: FindingPage,
});

function FindingPage() {
  return <h1>Finding</h1>;
}
