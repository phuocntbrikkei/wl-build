import * as pdfjsLib from 'pdfjs-dist/build/pdf';
import pdfjsWorker from 'pdfjs-dist/build/pdf.worker.min.js?url';

pdfjsLib.GlobalWorkerOptions.workerSrc = pdfjsWorker;

function decodeBase64(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

export function detectExamViewKind(fileName, mime, bytes) {
  if (bytes && bytes.length >= 4) {
    if (bytes[0] === 0x25 && bytes[1] === 0x50 && bytes[2] === 0x44 && bytes[3] === 0x46) return 'pdf';
    if (bytes[0] === 0x89 && bytes[1] === 0x50 && bytes[2] === 0x4e && bytes[3] === 0x47) return 'image';
    if (bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) return 'image';
    if (bytes[0] === 0x47 && bytes[1] === 0x49 && bytes[2] === 0x46) return 'image';
  }
  const name = String(fileName || '').toLowerCase();
  const type = String(mime || '').toLowerCase();
  if (type.includes('pdf') || name.endsWith('.pdf')) return 'pdf';
  if (type.startsWith('image/') || /\.(png|jpe?g|gif|webp)$/.test(name)) return 'image';
  if (type.startsWith('text/') || name.endsWith('.txt')) return 'text';
  return 'unsupported';
}

export async function renderSecurePdf(container, bytes) {
  container.innerHTML = '';
  const pdf = await pdfjsLib.getDocument({ data: bytes }).promise;
  const pad = 16;
  const width = container.clientWidth || container.parentElement?.clientWidth || 900;
  const maxWidth = Math.max(360, width - pad * 2);

  for (let pageNum = 1; pageNum <= pdf.numPages; pageNum += 1) {
    const page = await pdf.getPage(pageNum);
    const base = page.getViewport({ scale: 1 });
    const scale = Math.min(1.6, maxWidth / base.width);
    const viewport = page.getViewport({ scale });

    const wrap = document.createElement('div');
    wrap.className = 'exam-pdf-page-wrap';
    const canvas = document.createElement('canvas');
    canvas.className = 'exam-pdf-page';
    canvas.width = viewport.width;
    canvas.height = viewport.height;
    wrap.appendChild(canvas);
    container.appendChild(wrap);

    await page.render({
      canvasContext: canvas.getContext('2d'),
      viewport,
    }).promise;
  }
}

export function renderSecureImage(container, bytes, mime) {
  container.innerHTML = '';
  const blob = new Blob([bytes], { type: mime || 'image/png' });
  const img = document.createElement('img');
  img.className = 'exam-view-image';
  img.alt = 'Tài nguyên';
  img.draggable = false;
  img.src = URL.createObjectURL(blob);
  container.appendChild(img);
}

export function renderSecureText(container, bytes) {
  container.innerHTML = '';
  const pre = document.createElement('pre');
  pre.className = 'exam-view-text';
  pre.textContent = new TextDecoder('utf-8').decode(bytes);
  container.appendChild(pre);
}

export function decodeExamFilePayload(payload) {
  return decodeBase64(payload.data || payload);
}
