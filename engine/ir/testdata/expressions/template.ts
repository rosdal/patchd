function query(id: string, table: string) {
  return `SELECT * FROM ${table} WHERE id = ${id}`;
}
