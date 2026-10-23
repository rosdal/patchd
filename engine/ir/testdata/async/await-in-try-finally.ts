async function f(conn: any) {
  try {
    return await conn.run();
  } finally {
    await conn.close();
  }
}
