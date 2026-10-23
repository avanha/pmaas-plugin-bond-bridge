"use strict";
// Tests for content/static/js/bondbridge.js, with a document and a request that are fakes. Run with: node js_test/bondbridge_test.js
// (the Go test in oldbrowsers_test.go does).
var assert = require("assert");
var fs = require("fs");
var script = require("../content/static/js/bondbridge.js");

var tests = [];
function test(name, fn) { tests.push([name, fn]); }

function el(className, dataset) {
    return {className: className || "", dataset: dataset || {}, textContent: "", disabled: false, parentNode: null, listeners: {},
        addEventListener: function (type, fn) { this.listeners[type] = fn; }};
}

// A card with a fan part and a light part, each with a button, and the page it is on.
function fakePage(busy) {
    var card = el("entity-bond-device", {device: "fan1"});
    var error = el("error");
    var fan = el("part fan", {target: "fan"});
    var light = el("part light", {target: "light"});
    fan.parentNode = card;
    light.parentNode = card;
    var speed = el("cmd", {op: "speed", value: "2"});
    speed.parentNode = fan;
    var step = el("cmd", {op: "step", value: "-10"});
    step.parentNode = light;
    var hold = el("cmd", {op: "hold", value: "3"});
    hold.parentNode = light;
    card.querySelector = function () { return error; };
    card.querySelectorAll = function () { return [speed, step, hold]; };

    var doc = {
        querySelectorAll: function () { return [card]; },
        querySelector: function () { return busy ? el("busy") : null; }
    };

    return {doc: doc, error: error, speed: speed, step: step, hold: hold, buttons: [speed, step, hold]};
}

function bond(page) {
    var posts = [];
    var laters = [];
    var b = new script.BondDevices(page.doc, {
        post: function (url, body, done) { posts.push({url: url, body: body, done: done}); },
        later: function (fn, ms) { laters.push({fn: fn, ms: ms}); },
        reload: function () {}
    });

    return {b: b, posts: posts, laters: laters};
}

test("a click posts the command with the part it is in, and loads the page again once it has been sent", function () {
    var page = fakePage(false);
    var t = bond(page);
    assert.strictEqual(t.b.init(), true);

    page.speed.listeners.click();

    assert.strictEqual(t.posts[0].url, "/plugins/bondbridge/command");
    assert.deepStrictEqual(t.posts[0].body, {Device: "fan1", Target: "fan", Op: "speed", Value: 2});
    assert.ok(page.buttons.every(function (b) { return b.disabled; }));

    t.posts[0].done(null, 200, "");
    assert.strictEqual(t.laters.length, 1);
    assert.strictEqual(t.laters[0].ms, 1200);
});

test("a dimming is waited for longer, and a hold is a light's command with its seconds", function () {
    var page = fakePage(false);
    var t = bond(page);
    t.b.init();

    page.step.listeners.click();
    assert.deepStrictEqual(t.posts[0].body, {Device: "fan1", Target: "light", Op: "step", Value: -10});
    t.posts[0].done(null, 200, "");
    assert.strictEqual(t.laters[0].ms, 2500);

    page.hold.listeners.click();
    assert.deepStrictEqual(t.posts[1].body, {Device: "fan1", Target: "light", Op: "hold", Value: 3});
});

test("a refusal is said on the page, in the server's words, and the buttons come back", function () {
    var page = fakePage(false);
    var t = bond(page);
    t.b.init();

    page.speed.listeners.click();
    t.posts[0].done(null, 400, "Failed to handle request: the light is being dimmed: wait for it to finish\nmore");

    assert.strictEqual(page.error.textContent, "Could not send the command (400): the light is being dimmed: wait for it to finish");
    assert.ok(page.buttons.every(function (b) { return !b.disabled; }));
    assert.strictEqual(t.laters.length, 0);

    page.speed.listeners.click();
    t.posts[1].done(new Error("the request could not be sent"));
    assert.ok(page.error.textContent.indexOf("Could not send the command: ") === 0);
    assert.ok(page.buttons.every(function (b) { return !b.disabled; }));
});

test("while a light is being dimmed the page is loaded again after a second", function () {
    var t = bond(fakePage(true));
    t.b.init();
    assert.strictEqual(t.laters.length, 1);
    assert.strictEqual(t.laters[0].ms, 1000);

    t = bond(fakePage(false));
    t.b.init();
    assert.strictEqual(t.laters.length, 0);
});

test("init says whether there was a card on the page", function () {
    var b = new script.BondDevices({querySelectorAll: function () { return []; }, querySelector: function () { return null; }}, {});
    assert.strictEqual(b.init(), false);
});

test("the part of a button is found by walking up, and there may be none", function () {
    var part = el("part light", {target: "light"});
    var inner = el("x");
    var button = el("cmd");
    inner.parentNode = part;
    button.parentNode = inner;
    assert.strictEqual(script.partOf(button), part);
    assert.strictEqual(script.partOf(el("cmd")), null);
});

// The lint every script's test has: what Safari 9 does not have, looked for in the source. Pasted into each test.
test("the script uses nothing Safari 9 does not have", function () {
    var source = fs.readFileSync(__dirname + "/../content/static/js/bondbridge.js", "utf8").replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");

    [
        [/=>/, "an arrow function"], [/\bclass\s+\w/, "a class"], [/\blet\s/, "let"], [/\bconst\s/, "const"], [/`/, "a template literal"],
        [/\basync\b/, "async"], [/\bawait\b/, "await"], [/\bfetch\s*\(/, "fetch"], [/\.\.\./, "spread"], [/\?\./, "optional chaining"],
        [/\?\?/, "nullish coalescing"], [/\bObject\.(entries|values)\b/, "Object.entries or values"], [/\.padStart\b|\.padEnd\b/, "padStart"]
    ].forEach(function (pair) {
        assert.ok(!pair[0].test(source), "the script has " + pair[1]);
    });

    // forEach is only for arrays: a NodeList has none before Safari 10, which is what toArray is for.
    (source.match(/(\w+)\.forEach/g) || []).forEach(function (use) {
        assert.ok(["cards.forEach", "buttons.forEach"].indexOf(use) >= 0, "forEach on " + use);
    });
});

var failed = 0;
tests.forEach(function (t) {
    try { t[1](); console.log("ok   " + t[0]); } catch (e) { failed++; console.log("FAIL " + t[0] + "\n     " + (e && e.stack || e)); }
});
if (failed) { process.exit(1); }
