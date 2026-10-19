import * as vscode from "vscode";

// Later: spawn `patchd lsp` and connect a language client to it.
export function activate(context: vscode.ExtensionContext): void {
  const log = vscode.window.createOutputChannel("Patchd");
  context.subscriptions.push(
    log,
    vscode.commands.registerCommand("patchd.hello", () => {
      void vscode.window.showInformationMessage("Hello from Patchd");
    }),
  );
  log.appendLine("Patchd activated");
}

export function deactivate(): void {}
