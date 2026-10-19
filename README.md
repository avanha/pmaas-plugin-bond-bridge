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

1. `DimRamp`: how long the light takes from its dimmest to its brightest while held. Time it once with the remote. Without it, the
   light is on/off only.
2. **Calibration.** Nothing reads the brightness, so the plugin must be told where the dimmer is: put the light at its dimmest (or
   brightest) and press the button on the page. From then on it follows its own holds.
3. A guess about the remote: that each hold starts the other way from the last (`DimKeepsDirection` for one that doesn't).

The brightness shown is an estimate, and drifts with every hold, more with a laggy network. A level the wrong way from the next
hold is reached with a very short hold first, which turns the dimmer round. If a stop is lost the plugin tries again, and if it
can't, it logs loudly, forgets where the dimmer is, and the light may keep sweeping until stopped from the remote.

Not done: UDP push from the bridge (instant updates from other apps), the bridge's scenes and schedules, and remembering the
dimmer's position across restarts (it has to be calibrated again).
