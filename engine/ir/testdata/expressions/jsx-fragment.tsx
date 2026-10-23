declare const React: any;
export function List(p: { items: string[] }) {
  return (
    <>
      {p.items.map((i) => (
        <li key={i}>{i}</li>
      ))}
    </>
  );
}
