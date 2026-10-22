declare const db: any;
export async function handler(req: any, res: any) {
  const rows = await db.query(`SELECT * FROM users WHERE id = ${req.query.id}`);
  res.json(rows);
}
