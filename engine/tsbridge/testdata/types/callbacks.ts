// Contextual typing of callback parameters
interface Request {
  query: Record<string, string>;
}
interface Response {
  send(body: string): void;
}
interface App {
  get(path: string, handler: (req: Request, res: Response) => void): void;
}
declare const app: App;
app.get("/", (req, res) => {
  //          ^? Request
  res.send(req.query.name);
  //  ^? (body: string) => void
});
[1, 2, 3].map((x) => x * 2);
//             ^? number
