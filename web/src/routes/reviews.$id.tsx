import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/reviews/$id")({
  component: ReviewPage,
});

function ReviewPage() {
  return <h1>Review</h1>;
}
