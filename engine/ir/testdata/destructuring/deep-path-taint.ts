export function handler(req: any, res: any) {
  const {
    body: {
      user: {
        name,
        roles: [primary],
      },
    },
  } = req;
  res.send(name + primary);
}
