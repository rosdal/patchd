async function f(db: any, id: string) {
  try {
    const row = await db.query("SELECT * FROM t WHERE id = $1", [id]);
    return row;
  } catch (e) {
    return null;
  }
}
