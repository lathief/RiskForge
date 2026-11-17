// RiskForge Swagger UI bootstrap. Kept out of index.html so the page's Content-Security-Policy
// can forbid inline scripts. No credentials live here: use the "Authorize" button with a token
// obtained from POST /api/v1/auth/login.
window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "/openapi.yaml",
    dom_id: "#swagger-ui",
    deepLinking: true,
    // Do not keep the bearer token in localStorage between page loads.
    persistAuthorization: false,
    displayRequestDuration: true,
    tryItOutEnabled: false,
    // Never fetch a spec named in the page URL (?url=...), only the local contract.
    queryConfigEnabled: false,
    validatorUrl: null,
  });
});
