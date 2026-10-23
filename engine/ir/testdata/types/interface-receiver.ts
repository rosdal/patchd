interface Db {
  query(sql: string): Promise<{ rows: string[] }>;
}
export async function rows(db: Db, id: string) {
  const res = await db.query(`SELECT ${id}`);
  return res.rows;
}
