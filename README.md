# uspace-lab
U-space system-of-systems: specification, integration, SITL scenarios, load tests and demo

## Live demo hosts

The staging droplet serves the five systems over HTTPS (Caddy, deployed
by `uspace-deploy`):

- https://uspace-authority.chikox.net: the competent authority
- https://uspace-cisp.chikox.net: the common information service provider
- https://uspace-ussp.chikox.net: the U-space service provider
- https://uspace-ansp.chikox.net: the air navigation service provider
- https://uspace-lab.chikox.net: the lab host, the InterUSS DSS (`/rid/*`,
  `/dss/*`, `/aux/*`, `/healthy`) and the lab token issuer
  (`/oauth/token`, `/.well-known/jwks.json`); every other path answers 404

Roles: `docs/spec/00-overview.md` section 3. Hosts: `docs/spec/05-scale-and-reliability.md`.
