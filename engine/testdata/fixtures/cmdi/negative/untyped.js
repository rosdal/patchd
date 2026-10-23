// Plain JavaScript that only looks like a handler, or uses safe calls.
const { exec, execFile } = require("child_process");

// One parameter: not the (req, res) shape.
function single(req) {
  exec("echo " + req.cmd); // expect-none
}

// (request, next) is not a handler shape the heuristic accepts.
function middleware(request, next) {
  exec("echo " + request.query.x); // expect-none
  next();
}

module.exports.safe = function (req, res) {
  execFile("ping", ["-c", "1", req.query.host]); // expect-none
  exec("uptime"); // expect-none
  res.send("ok");
};

module.exports = { single, middleware };
