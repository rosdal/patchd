import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/security")({
  component: SecurityPage,
});

function SecurityPage() {
  return <h1>Security</h1>;
}
