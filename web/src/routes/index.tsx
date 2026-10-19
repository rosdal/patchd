import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/")({
  component: Inbox,
});

function Inbox() {
  return <h1>Inbox</h1>;
}
