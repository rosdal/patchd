// From https://github.com/microsoft/vscode src/vs/base/common/functional.ts at 052acf011dbc.
// Licence: MIT, Copyright (c) 2015 - present Microsoft Corporation. See THIRD_PARTY_NOTICES.md.
/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

/**
 * Given a function, returns a function that is only calling that function once.
 */
export function createSingleCallFunction<T extends Function>(this: unknown, fn: T, fnDidRunCallback?: () => void): T {
	const _this = this;
	let didCall = false;
	let result: unknown;

	return function () {
		if (didCall) {
			return result;
		}

		didCall = true;
		if (fnDidRunCallback) {
			try {
				result = fn.apply(_this, arguments);
			} finally {
				fnDidRunCallback();
			}
		} else {
			result = fn.apply(_this, arguments);
		}

		return result;
	} as unknown as T;
}
