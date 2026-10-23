// A raw Node http server: IncomingMessage.url and .headers are sources.
import http from "node:http";
import { exec } from "node:child_process";

http
  .createServer((req, res) => {
    exec("echo " + req.url); // expect: ts/cmdi
    exec("echo " + req.headers.host); // expect: ts/cmdi
    res.end();
  })
  .listen(8080);
