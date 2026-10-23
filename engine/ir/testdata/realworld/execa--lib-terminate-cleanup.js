// From https://github.com/sindresorhus/execa lib/terminate/cleanup.js at e7e0664c25e3.
// Licence: MIT, Copyright (c) Sindre Sorhus. See THIRD_PARTY_NOTICES.md.
import {addAbortListener} from 'node:events';
import {onExit} from 'signal-exit';

// If the `cleanup` option is used, call `subprocess.kill()` when the parent process exits
export const cleanupOnExit = (kill, {cleanup, detached}, {signal}) => {
	if (!cleanup || detached) {
		return;
	}

	const removeExitHandler = onExit(() => {
		kill();
	});
	addAbortListener(signal, () => {
		removeExitHandler();
	});
};
