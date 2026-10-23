declare const React: any;
export function Raw(p: { html: string }) {
  return <div dangerouslySetInnerHTML={{ __html: p.html }} />;
}
