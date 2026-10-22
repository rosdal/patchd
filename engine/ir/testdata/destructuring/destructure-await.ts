async function f(db: any) {
  const { rows } = await db.query("SELECT 1");
  return rows;
}
