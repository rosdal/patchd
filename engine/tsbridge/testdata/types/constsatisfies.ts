// as const and satisfies
const routes = { home: "/", about: "/about" } as const;
//    ^? { readonly home: "/"; readonly about: "/about"; }
const config = { port: 8080, host: "localhost" } satisfies Record<string, string | number>;
//    ^? { port: number; host: string; }
const port = config.port;
//    ^? number
export { routes, port };
