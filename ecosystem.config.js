// pm2 process supervisor config for local dev services — restarts them
// automatically if they crash. Start with: pm2 start ecosystem.config.js
//
// Part of the service-watchdog mechanism (see scripts/docker-watchdog.ps1):
// pm2 handles instant restart-on-crash for these two processes; the separate
// PowerShell watchdog (run on a schedule) handles Docker Desktop itself
// (which isn't a plain child process pm2 can supervise) and acts as a
// safety net if pm2's own apps get stuck in a non-'online' state.
module.exports = {
  apps: [
    {
      // pm2 can't exec `npm` directly on Windows (npm.cmd isn't a script node's
      // module loader can run) — go through cmd.exe, the standard workaround.
      name: 'sis-frontend',
      cwd: 'C:/Users/123/Projects/sis/frontend',
      script: 'cmd.exe',
      args: '/c npm run dev',
      autorestart: true,
      max_restarts: 50,
      restart_delay: 3000,
    },
    {
      name: 'sis-api-gateway',
      // `go run` from the repo root so relative paths (.env, etc.) resolve
      // the same way as when run by hand — matches how it's normally started.
      cwd: 'C:/Users/123/Projects/sis',
      script: 'go',
      args: 'run ./services/api-gateway',
      autorestart: true,
      max_restarts: 50,
      restart_delay: 3000,
    },
  ],
}
