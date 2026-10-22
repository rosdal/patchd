declare const React: any;
export function Greeting(props: { name: string }) {
  return <div className="greeting">Hello, {props.name}!</div>;
}
