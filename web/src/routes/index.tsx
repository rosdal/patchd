import { createFileRoute } from "@tanstack/react-router";
import { Placeholder } from "../components/placeholder";

export const Route = createFileRoute("/")({
  component: () => <Placeholder version={__PATCHD_VERSION__} />,
});
