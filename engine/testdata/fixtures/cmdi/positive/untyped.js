// Plain JavaScript handlers: req is untyped, so the (req, res) shape marks
// it as an Express request (ADR 0010), and require gives the sinks.
const { exec } = require("child_process");
const cp = require("child_process");
const execSync = require("child_process").execSync;

module.exports.ping = function (req, res) {
  exec("ping -c 2 " + req.body.address, function (err, stdout) { // expect: ts/cmdi
    res.send(stdout);
  });
};

module.exports.list = function (req, res) {
  cp.exec("ls " + req.query.dir); // expect: ts/cmdi
};

exports.run = (req, res) => {
  execSync(req.params.cmd); // expect: ts/cmdi
};

function lookup(request, response) {
  exec(`dig ${request.query.name}`); // expect: ts/cmdi
  response.end();
}
module.exports.lookup = lookup;
