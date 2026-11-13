'use strict';
(async function () {
  const status = document.getElementById('load-status');
  try {
    const base = document.body.dataset.docsBase;
    const manifestResponse = await fetch(`${base}/catalog.json`, { credentials: 'omit' });
    if (!manifestResponse.ok) throw new Error('The API guide catalog is unavailable.');
    const catalog = await manifestResponse.json();
    const requested = new URLSearchParams(window.location.search).get('api');
    const guide = catalog.guides.find(item => item.id === requested) || catalog.guides[0];
    const picker = document.getElementById('api-picker');
    picker.replaceChildren(...catalog.guides.map(item => {
      const option = document.createElement('option');
      option.value = item.id; option.textContent = item.title;
      return option;
    }));
    picker.value = guide.id; picker.disabled = false;
    picker.addEventListener('change', () => { window.location.href = `/docs/?api=${encodeURIComponent(picker.value)}`; });
    const url = `${base}/contracts/${guide.file}`;
    const response = await fetch(url, { credentials: 'omit' });
    if (!response.ok) throw new Error('The selected API contract is unavailable.');
    const spec = await response.json();
    spec.servers = [{ url: window.location.origin, description: 'Current staging environment' }];
    spec.info.title = guide.title;
    document.title = `${guide.title} · Hello API`;
    document.getElementById('target-origin').textContent = window.location.origin;
    document.getElementById('operation-total').textContent = `${catalog.guides.reduce((sum, item) => sum + item.operations, 0)} operations`;
    const download = document.getElementById('download-spec');
    download.href = url; download.hidden = false;
    window.SwaggerUIBundle({
      spec, dom_id: '#swagger-ui', deepLinking: true, filter: true, docExpansion: 'list',
      defaultModelsExpandDepth: 0, displayRequestDuration: true,
      persistAuthorization: false, queryConfigEnabled: false, validatorUrl: null,
      tryItOutEnabled: false, useUnsafeMarkdown: false,
      requestInterceptor(request) {
        const target = new URL(request.url, window.location.origin);
        if (target.origin !== window.location.origin || target.username || target.password || !target.pathname.startsWith('/api/v1/')) {
          throw new Error('Requests must target this staging API.');
        }
        request.credentials = 'omit';
        return request;
      },
      onComplete() { status.textContent = `${guide.operations} operations · ${guide.description}`; }
    });
  } catch (error) {
    status.textContent = 'The API reference could not load. Reload the page or contact the staging maintainer.';
    status.classList.add('load-error');
  }
}());
