# elgato-mcp

An [MCP](https://modelcontextprotocol.io) server for controlling Elgato Key
Light Air (and Key Light) lights on your local network.

## Tools

- `list_lights`: shows each light's power, brightness (0-100) and colour temperature (Kelvin).
- `set_lights`: changes `on`, `brightness` and/or `temperature_kelvin` (2900-7000)
  for the named lights, or for every light if `lights` is left out. Only the
  settings you provide are changed.
- `discover_lights`: searches the local network for lights and adds any new
  ones. `list_lights` also runs discovery if no lights are known yet.

## Configuration

```sh
go install github.com/bigkevmcd/elgato-mcp@latest
```

### Automatic discovery

With no lights configured, the server finds them at startup using mDNS
(`_elg._tcp`). Discovered lights use the name set in the Elgato Control
Center app.

```sh
claude mcp add elgato -- elgato-mcp
```

`-discovery-timeout` sets how long to wait for lights to respond (default
`3s`); `-discovery-timeout 0` turns discovery off.

Your firewall must allow incoming mDNS (UDP port 5353). With firewalld:

```sh
sudo firewall-cmd --permanent --add-service=mdns
sudo firewall-cmd --reload
```

### Static configuration

You can also give the lights as a comma-separated list of `[name=]host[:port]`,
either with `-lights` or with the `ELGATO_LIGHTS` environment variable. The
port defaults to 9123. Discovery is still available through `discover_lights`.

```sh
claude mcp add elgato -- elgato-mcp -lights left=192.168.1.20,right=192.168.1.21
```

### Sandbox

Use `-sandbox` to run the same MCP tools against in-memory light states, without
discovering or contacting real devices. Changes reset when the process restarts.
The file contains a `lights` array; each entry needs a name, brightness, and
Kelvin temperature. `on` defaults to false and `product` is optional.

```sh
elgato-mcp -sandbox data/sandbox.json
```

```json
{
  "lights": [
    {
      "name": "Desk",
      "product": "Elgato Key Light Air",
      "on": true,
      "brightness": 45,
      "temperature_kelvin": 4000
    }
  ]
}
```

`-sandbox` cannot be combined with `-lights` or a non-empty `ELGATO_LIGHTS`.
The server identifies itself as `elgato-sandbox`; its `discover_lights` tool
returns only the lights in the sandbox file.
