# pmaas-plugin-bond-bridge
A plugin to integrate a [Bond Bridge](https://bondhome.io/product/bond-bridge/) into PMAAS, over its local HTTP API: ceiling fans
and their lights.

```go
conf := bondbridge.NewPluginConfig()
conf.Host = "10.47.6.39"
conf.Token = bondToken // Bond app: Settings, Advanced, Local Token. Keep it out of source control.

// Optional. Everything the bridge has is found and shown; this is for what to make of it.
conf.Options("Bedroom Ceiling Fan", bondbridge.DeviceOptions{Light: bondbridge.LightNone})
conf.Options("Living Room Ceiling Fan", bondbridge.DeviceOptions{DimRamp: 4 * time.Second})

livingRoomFan := conf.Fan("Living Room Ceiling Fan")     // a device.IFan, which is also a device.ISwitch
livingRoomLight := conf.Light("Living Room Ceiling Fan") // a device.ILight, also an ISwitch

coreConfig.AddPlugin(bondbridge.NewPlugin(conf), config.PluginConfig{})
```

The plugin asks the bridge what is paired with it, and makes a **fan** (`device.IFan`: on and off, speed, direction) and a **light**
(`device.ILight`: on and off, and a brightness) of each device that has them. Both are also switches, so an automation or a
button can turn them on and off like any other (`automations.Flip(livingRoomLight)`). A page lists the devices with buttons.

* **Which light.** A fan may have an up light and a down light, with one fitted. The down light is used if there is one;
  `DeviceOptions.Light` picks another or `LightNone`. `Hidden` leaves a device out.
* **State is assumed.** A ceiling fan on RF can't report back: the bridge knows only what it last sent. Changes made with the
  fan's own remote are never seen, here or in the Bond app. The page and `Assumed` in the data say so. The plugin asks for the
  bridge's state every `PollEvery` (30 seconds), and right after each command it sends.
* **Correcting the bridge.** When what it believes is wrong, the page can tell it the truth ("It's really off", "The fan is off", "Speed 2"): a `PATCH` of the
  bridge's state, which transmits nothing. This matters for a light that is switched by a toggle: a command to turn it on is not sent if the
  bridge believes it is on already.
* **Offline.** If the bridge doesn't answer, the devices are offline (their last state is kept) and commands are refused with
  an error. Finding the devices is tried again every 30 seconds until the bridge answers.
* **Calls are made one at a time**, off the plugin's goroutine, because the bridge is a small device.

## Dimming (experimental)

The bridge **cannot be told a brightness**. It can only *start the dimmer* and *stop it*, as holding the dim button on the remote
does, and the light then sweeps up and down, turning round at the ends, until it is stopped. (That is why the app is hard to use.)
So the plugin reaches a brightness by timing a hold, which needs three things:

0. `DimDelay`: how long the dimmer has to be held before the light starts to move (default 1 second). A shorter hold is taken by the
   fan for a button press, which switches the light on or off, so the plugin never holds for less, and doesn't count it as movement.
0. `DimMinHold`: the shortest hold made (default 1.4 seconds). Measured on a bedroom fan, 1.25 s was safe and about 1.1 s switched the
   light. So the finest step is what that hold moves, about 10%, and a smaller change is rounded to nothing or to a step.
0. `DimCooldown`: the rest after each hold (default 1 second), before the next hold or a new command; the fan's receiver is easily
   confused by commands close together. The light is busy meanwhile: the page disables its buttons, and a command from the page is
   refused instead of queued.
1. `DimRamp`: how long the light takes from its dimmest to its brightest while held. Time it once with the remote. Without it, the
   light is on/off only.
2. **Calibration.** Nothing reads the brightness, so the plugin must be told where the dimmer is: put the light at its dimmest (or
   brightest) and press the button on the page. From then on it follows its own holds.
3. A guess about the remote: that each hold starts the other way from the last (`DimKeepsDirection` for one that doesn't).

The brightness shown is an estimate, and drifts with every hold, more with a laggy network. A level the wrong way from the next
hold is reached with a very short hold first, which turns the dimmer round. If a stop is lost the plugin tries again, and if it
can't, it logs loudly, forgets where the dimmer is, and the light may keep sweeping until stopped from the remote.

**Hold buttons.** Next to the brightness buttons the page has 2s to 6s, which hold the dimmer for that long. The light starts to move only after
`DimDelay`, so with the defaults a 2s hold moves it for about 1.2 s (roughly 20% on the 6 second ramp) and a 6s hold for about 5.2 s. Nothing
shorter than `DimMinHold` is offered or accepted, because the fan takes a shorter hold for a press of the button, which switches the light.
They work whether or not the brightness is known, which is the way to move it by sight. The direction can't be chosen: a hold carries on the
way the last went (with `DimKeepsDirection`) and turns round at an end, so the page says which way the next is expected to go, and has a
button to correct that if you see otherwise (nothing is sent to the light).

**Old browsers.** The page is used on an iPad that stopped at iOS 9 (Safari 9), so `bondbridge.js` is plain ES5 with `XMLHttpRequest` (a script that
uses an arrow function, a class field or `fetch` doesn't run there at all, and the buttons can't be clicked), and the stylesheet spaces the flex
containers' items with margins, not `gap`, which needs Safari 14.1. Tests in `internal/http` (`oldbrowsers_test.go`, `js_test/`) look for what
it doesn't have.

**Two commands for one dimmer.** Some fans, on firmware v4.39, have two recorded commands for each dimmer action ("Bottom Dimmer"
twice). The action runs the first, and the fan may ignore it. The plugin finds the command that sends the same signal as the
light's toggle (what the remote sends when the button is held) and transmits that one by its id.

Not done: UDP push from the bridge (instant updates from other apps), the bridge's scenes and schedules, and remembering the
dimmer's position across restarts (it has to be calibrated again).
