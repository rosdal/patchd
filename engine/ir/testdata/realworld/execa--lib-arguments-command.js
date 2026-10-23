// From https://github.com/sindresorhus/execa lib/arguments/command.js at e7e0664c25e3.
// Licence: MIT, Copyright (c) Sindre Sorhus. See THIRD_PARTY_NOTICES.md.
import {logCommand} from '../verbose/start.js';
import {getVerboseInfo} from '../verbose/info.js';
import {getStartTime} from '../return/duration.js';
import {joinCommand} from './escape.js';
import {normalizeFdSpecificOption} from './specific.js';

// Compute `result.command`, `result.escapedCommand` and `verbose`-related information
export const handleCommand = (filePath, rawArguments, rawOptions) => {
	const startTime = getStartTime();
	// The `node` option ignores `shell`
	const {command, escapedCommand} = joinCommand(filePath, rawArguments, rawOptions.shell && !rawOptions.node);
	const verbose = normalizeFdSpecificOption(rawOptions, 'verbose');
	// Keep the null prototype, otherwise a polluted `Object.prototype` would inject option values
	const verboseInfo = getVerboseInfo(verbose, escapedCommand, {__proto__: null, ...rawOptions});
	logCommand(escapedCommand, verboseInfo);
	return {
		command,
		escapedCommand,
		startTime,
		verboseInfo,
	};
};
