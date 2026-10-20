import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { Placeholder } from "./placeholder";

test("shows the product name and build version", () => {
  render(<Placeholder version="v0.1.0" />);
  expect(screen.getByRole("heading", { name: "Patchd Code" })).toBeTruthy();
  expect(screen.getByText("Version v0.1.0")).toBeTruthy();
});
