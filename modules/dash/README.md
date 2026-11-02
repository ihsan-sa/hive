# dash
The web app. It runs as the service `hive-dash` on 127.0.0.1 only. `hive web` prints its address, with the secret. Also `hive-dash` (text snapshot), `hive-dash --watch`, `hive-dash --json`.

What each request may do is checked on the server, from `~/.hive/web.json`:
- **Local, with the secret:** everything. The address exchanges the secret for an HttpOnly, SameSite=Strict cookie. Host and Origin are checked on every request. `hive web --new-secret` replaces it and ends every session.
- **Outside (through a tunnel):** refused until set up under Settings, Access from outside. Then every request must carry a `Cf-Access-Jwt-Assertion` token signed by your Cloudflare Access team, and can do only what's ticked there, read-only by default. Any forwarding header puts a request on this route, even with the cookie. Who may do what can't be changed from outside. Opening a hostname also takes a tunnel and an Access policy in Cloudflare.
- **No web.json, or one that can't be read:** nothing is served. Install makes the file; if it's lost, run `hive web --setup` and restart `hive-dash`.

Capabilities: read, post, stop, tier, limits, logins, config, modules. A post as the person can start agents with this account's full rights, so grant post from outside with care.
