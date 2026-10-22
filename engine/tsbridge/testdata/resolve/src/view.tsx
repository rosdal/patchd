export function Button(props: { label: string }) {
  return <div>{props.label}</div>;
}
export const page = <Button label="go" />; // resolves-to: local:src/view.tsx#Button
