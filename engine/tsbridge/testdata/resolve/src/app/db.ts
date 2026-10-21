import { Client } from "pg";

export function runQuery(sql: string) {
  const client = new Client(); // resolves-to: pg:Client
  return client.query(sql); // resolves-to: pg:Client.query
}

export default class Repo {
  find(id: number) {
    return runQuery(`select ${id}`); // resolves-to: local:src/app/db.ts#runQuery
  }
}
