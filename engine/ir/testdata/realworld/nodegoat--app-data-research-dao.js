// From https://github.com/OWASP/NodeGoat app/data/research-dao.js at c5cb68a7084e.
// Licence: Apache License 2.0, Copyright the OWASP NodeGoat contributors. See THIRD_PARTY_NOTICES.md.
/* The ResearchDAO must be constructed with a connected database object */
function ResearchDAO(db) {

    "use strict";

    /* If this constructor is called without the "new" operator, "this" points
     * to the global object. Log a warning and call it correctly. */
    if (false === (this instanceof ResearchDAO)) {
        console.log("Warning: ResearchDAO constructor called without 'new' operator");
        return new ResearchDAO(db);
    }

    this.getBySymbol = (symbol, callback) => {

        const searchCriteria = () => {

            if (symbol) {
                console.log("in if symbol");
                return {
                    symbol
                };
            }
        };
    };
}

module.exports = { ResearchDAO };
