export function register(app: any, db: any) {
  app.get("/u/:id", async (req: any, res: any) => {
    const u = await db.users.find(req.params.id);
    res.send(u.name);
  });
}
