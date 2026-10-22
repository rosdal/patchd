declare const React: any;
export function C(p: { show: boolean; text: string }) {
  return <p>{p.show && <b>{p.text}</b>}</p>;
}
