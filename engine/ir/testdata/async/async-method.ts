class Repo {
  async find(id: string) {
    return await this.db.get(id);
  }
  db: any;
}
