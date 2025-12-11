// Local environment configuration for the frontend
// Adjust apiBase if your server runs on a different host/port
window.APP_CONFIG = {
  apiBase: 'http://localhost:8080',
  // Optional MCP config passed to /api/llm when the toggle is on.
  // Replace with the path or contents of your MCP config. Example keeps it simple
  // by pointing at the local server MCP endpoint.
  mcpConfig: {
    endpoint: 'http://localhost:8080/api/mcp'
  }
};
