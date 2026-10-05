(() => {
  const dialog = document.getElementById('job-output-dialog');
  const frameContainer = document.getElementById('job-output-frame-container');
  const fullPageLink = document.getElementById('job-output-full-page');
  const closeButton = document.getElementById('job-output-close');
  const metadata = document.getElementById('job-output-metadata');
  const result = document.getElementById('job-output-result');
  const status = document.getElementById('job-output-status');
  const heading = document.getElementById('job-output-heading');

  if (!dialog || !frameContainer || !fullPageLink || !closeButton || !dialog.showModal) return;

  let invokingLink = null;
  let bridgedFrameWindow = null;
  let frameEscapeHandler = null;

  function jobViewURL(link) {
    let url;
    try {
      url = new URL(link.href, document.baseURI);
      if (url.origin !== window.location.origin || url.username || url.password || url.search || url.hash) return null;
      const route = url.pathname.match(/^(.*\/)jobs\/([^/]+)$/);
      if (!route) return null;
      const jobID = decodeURIComponent(route[2]);
      if (!jobID || jobID === '.' || jobID === '..' || jobID.includes('/') || jobID.includes('\\')) return null;
    } catch (_) {
      return null;
    }
    return url;
  }

  function removeFrame() {
    const frame = frameContainer.querySelector('iframe');
    if (frame) {
      if (bridgedFrameWindow && frameEscapeHandler) {
        try {
          bridgedFrameWindow.removeEventListener('keydown', frameEscapeHandler, true);
        } catch (_) {
          // A navigation may have changed the iframe origin since it loaded.
        }
      }
      bridgedFrameWindow = null;
      frameEscapeHandler = null;
      frame.src = 'about:blank';
      frame.remove();
    }
  }

  function openOutput(link) {
    const url = jobViewURL(link);
    if (!url) return false;

    removeFrame();
    invokingLink = link;
    const operation = link.dataset.operation || 'Job';
    const repository = link.dataset.repository || 'Repository unavailable';
    const workspace = link.dataset.workspace || 'Workspace unavailable';
    const project = link.dataset.project || 'Project unavailable';
    const timestamp = link.dataset.timestamp || 'Time unavailable';
    const executionStatus = link.dataset.status || 'Unknown';
    const counts = link.dataset.counts || '';

    heading.textContent = `${operation} output`;
    metadata.textContent = `Repository: ${repository} · Workspace: ${workspace} · Project: ${project} · ${timestamp}`;
    result.textContent = `Status: ${executionStatus}${counts ? ` · ${counts}` : ''}`;
    status.textContent = 'Loading this execution’s terminal…';
    fullPageLink.href = url.href;

    if (!dialog.open) dialog.showModal();

    const frame = document.createElement('iframe');
    frame.className = 'job-output-frame';
    frame.title = `${operation} output for ${repository}, ${workspace}, ${project}`;
    frame.addEventListener('load', () => {
      if (!frame.isConnected) return;
      status.textContent = 'Terminal page loaded. This does not confirm a WebSocket connection or job success.';
      try {
        const frameWindow = frame.contentWindow;
        if (!frameWindow) return;
        frameEscapeHandler = event => {
          if (event.key !== 'Escape' || !dialog.open) return;
          event.preventDefault();
          event.stopPropagation();
          dialog.close();
        };
        frameWindow.addEventListener('keydown', frameEscapeHandler, true);
        bridgedFrameWindow = frameWindow;
      } catch (_) {
        // Cross-origin authentication redirects keep the full-page link available.
      }
    });
    frameContainer.appendChild(frame);
    frame.src = url.href;
    return true;
  }

  function finishClose() {
    removeFrame();
    status.textContent = 'Select an execution to view its output.';
    if (invokingLink && invokingLink.isConnected) invokingLink.focus();
    invokingLink = null;
  }

  document.addEventListener('click', event => {
    const link = event.target.closest && event.target.closest('a[data-output-link]');
    if (!link || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    if (link.target && link.target !== '_self' || link.hasAttribute('download')) return;
    if (openOutput(link)) event.preventDefault();
  });

  closeButton.addEventListener('click', () => dialog.close());
  dialog.addEventListener('cancel', event => {
    event.preventDefault();
    dialog.close();
  });
  dialog.addEventListener('click', event => {
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener('close', finishClose);
  window.addEventListener('pagehide', removeFrame);
})();
