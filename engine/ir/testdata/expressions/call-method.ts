function f(db: { query(q: string): unknown }) {
  return db.query("SELECT 1");
}
