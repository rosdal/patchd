import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/rules")({
  component: RulesPage,
});

function RulesPage() {
  return <h1>Rules</h1>;
}
