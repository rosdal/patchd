function f(users: { name: string; age: number }[]) {
  for (const { name, age } of users) console.log(name, age);
}
