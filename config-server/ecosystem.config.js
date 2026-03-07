module.exports = {
  apps: [
    {
      name: 'nexgen-client',
      cwd: __dirname,
      script: './run-nexgen-client.sh',
      interpreter: '/bin/sh',
      exec_mode: 'fork',
      instances: 1,
      autorestart: true,
      watch: false,
      time: true,
      max_restarts: 10,
      min_uptime: '5s',
      env: {
        NODE_ENV: 'production'
      }
    }
  ]
}
