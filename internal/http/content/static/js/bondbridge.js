/*
 * The buttons on the Bond Bridge page. A button asks for something to be done; the bridge does it a moment later, so the page is loaded again
 * once it has had time to.
 *
 * It runs on old browsers, an iPad that no longer gets updates among them (Safari 9): plain ES5, with no arrow functions, classes, class fields,
 * let or const, async functions or fetch, none of which it has, and a script that uses one of them does not run at all, which is a page of
 * buttons that cannot be clicked. Nor does it have NodeList.forEach. Nothing here is slower or worse in a modern browser.
 *
 * The parts are given what they use from outside (the document, how a request is made, how the page is reloaded), so that they are tested
 * with fakes: see js_test/bondbridge_test.js.
 */
(function (global) {
    "use strict";

    // A NodeList from the old browsers has no forEach.
    function toArray(list) {
        return Array.prototype.slice.call(list);
    }

    // Posts the body as JSON, and calls done(error, status, text), error being set if there was no answer at all.
    function postJson(url, body, done) {
        var request = new global.XMLHttpRequest();

        request.open("POST", url, true);
        request.setRequestHeader("Content-Type", "application/json");

        request.onload = function () {
            done(null, request.status, request.responseText);
        };

        request.onerror = function () {
            done(new Error("the request could not be sent"));
        };

        request.send(JSON.stringify(body));
    }

    // What the server says about a request it refused is about the request, so it is worth showing: its first line, without its prefix.
    function reasonFrom(text) {
        return String(text).replace(/^Failed to handle request: /, "").split("\n")[0].slice(0, 200);
    }

    // The part of a card (the fan or the light) that holds the button, found by walking up: Element.closest is newer than the oldest Safari.
    function partOf(button) {
        var node = button.parentNode;

        while (node && node.className !== undefined) {
            if ((" " + node.className + " ").indexOf(" part ") >= 0) {
                return node;
            }

            node = node.parentNode;
        }

        return null;
    }

    function BondDevices(doc, options) {
        options = options || {};
        this.doc = doc;
        this.post = options.post || postJson;
        this.later = options.later || global.setTimeout.bind(global);
        this.reload = options.reload || function () {
            global.location.reload();
        };
    }

    BondDevices.prototype.init = function () {
        var self = this;
        var cards = toArray(this.doc.querySelectorAll("div.entity-bond-device"));

        cards.forEach(function (card) {
            toArray(card.querySelectorAll("button.cmd")).forEach(function (button) {
                button.addEventListener("click", function () {
                    self.send(card, button);
                });
            });
        });

        // While a light is being dimmed the page is loaded again until it is done, which enables its buttons.
        if (this.doc.querySelector(".busy")) {
            this.later(this.reload, 1000);
        }

        return cards.length > 0;
    };

    BondDevices.prototype.send = function (card, button) {
        var self = this;
        var error = card.querySelector(".error");
        var buttons = toArray(card.querySelectorAll("button.cmd"));
        var part = partOf(button);
        error.textContent = "";

        var body = {
            Device: card.dataset.device,
            Target: part ? part.dataset.target : "",
            Op: button.dataset.op,
            Value: parseInt(button.dataset.value, 10)
        };

        function setDisabled(disabled) {
            buttons.forEach(function (b) {
                b.disabled = disabled;
            });
        }

        setDisabled(true);

        this.post("/plugins/bondbridge/command", body, function (failure, status, text) {
            if (failure) {
                error.textContent = "Could not send the command: " + failure;
                setDisabled(false);
                return;
            }

            if (status < 200 || status > 299) {
                error.textContent = "Could not send the command (" + status + "): " + reasonFrom(text);
                setDisabled(false);
                return;
            }

            // A dimming takes a while. The page shows it is dimming, and the buttons come back when it is loaded again.
            self.later(self.reload, body.Op === "brightness" || body.Op === "step" ? 2500 : 1200);
        });
    };

    var exported = {BondDevices: BondDevices, reasonFrom: reasonFrom, partOf: partOf};

    if (typeof module !== "undefined" && module.exports) {
        module.exports = exported;
    }

    if (typeof document !== "undefined" && global.addEventListener) {
        new BondDevices(document).init();
    }
})(typeof window !== "undefined" ? window : globalThis);
