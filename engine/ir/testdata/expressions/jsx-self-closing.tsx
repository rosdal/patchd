declare const React: any;
declare function Avatar(p: { src: string }): any;
export function User(p: { url: string }) {
  return <Avatar src={p.url} />;
}
