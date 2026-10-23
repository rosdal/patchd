const config = { secret: "x" };
export function makeHandler(db: any) {
  return (req: any, res: any) => {
    db.query(req.query.sql, config.secret);
    res.end();
  };
}
