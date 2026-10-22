function f(req: any) {
  const {
    body: {
      user: { name },
    },
  } = req;
  return name;
}
