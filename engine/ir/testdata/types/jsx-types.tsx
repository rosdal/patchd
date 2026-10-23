declare namespace JSX {
  interface IntrinsicElements {
    div: { id?: string };
  }
}
declare function jsx(...a: unknown[]): unknown;
export const el = <div id="x" />;
