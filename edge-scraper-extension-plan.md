# MS Edge Web Scraper Extension - Implementation Plan

## Project Overview

**Goal**: Create an MS Edge extension that scrapes data from dozens of URLs with dynamic JavaScript content and saves results as JSON files to a user-selected folder.

**Requirements:**
- Handle dynamic JS-rendered content
- Sequential URL processing (one at a time)
- Save data as JSON files
- User-friendly UI for managing URL list and triggering scraping

**Architecture**: Pure Browser Extension (Manifest V3)

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        MS Edge Extension                         │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐    ┌──────────────────┐    ┌──────────────┐ │
│  │   Popup UI   │    │  Service Worker  │    │ Content      │ │
│  │              │◄───┤  (background.js) │───►│ Script       │ │
│  │ - Add URLs   │    │                  │    │ (scraper.js) │ │
│  │ - View list  │    │ - Queue manager  │    │              │ │
│  │ - Start/Stop │    │ - Alarms (keep   │    │ - DOM parse  │ │
│  │ - Progress   │    │   alive)         │    │ - Extract    │ │
│  │ - Settings   │    │ - File saving    │    │ - Send back  │ │
│  └──────────────┘    └──────────────────┘    └──────────────┘ │
│         │                     │                       │        │
│         │                     │                       │        │
│         ▼                     ▼                       ▼        │
│  ┌──────────────┐    ┌──────────────────┐    ┌──────────────┐ │
│  │chrome.storage│    │chrome.alarms     │    │chrome.tabs   │ │
│  └──────────────┘    └──────────────────┘    └──────────────┘ │
│                                                         │        │
└─────────────────────────────────────────────────────────┼────────┘
                                                          │
                                                          ▼
                                               ┌──────────────────┐
                                               │ File System      │
                                               │ Access API       │
                                               │ (showDirectory   │
                                               │  Picker)         │
                                               └──────────────────┘
                                                          │
                                                          ▼
                                               ┌──────────────────┐
                                               │ User-selected    │
                                               │ folder (JSON     │
                                               │  files)          │
                                               └──────────────────┘
```

---

## File Structure

```
edge-scraper-extension/
├── manifest.json              # Extension configuration (Manifest V3)
│
├── background.js              # Service worker - main orchestration
├── background-utils.js        # Helper functions for background
│
├── popup/
│   ├── popup.html             # Popup UI structure
│   ├── popup.js               # Popup logic and communication
│   └── popup.css              # Popup styling
│
├── content/
│   ├── scraper.js             # Content script - DOM scraping logic
│   └── selectors.js           # CSS selectors for data extraction
│
├── lib/
│   ├── queue-manager.js       # URL queue management
│   ├── parser.js              # Data parsing and normalization
│   └── file-handler.js        # File System Access API wrapper
│
├── icons/
│   ├── icon16.png
│   ├── icon48.png
│   └── icon128.png
│
└── README.md                  # Setup and usage instructions
```

---

## Implementation Steps

### Phase 1: Project Setup & Manifest

**File**: `manifest.json`

```json
{
  "manifest_version": 3,
  "name": "Web Data Scraper",
  "version": "1.0.0",
  "description": "Scrape data from multiple URLs and save as JSON",
  "permissions": [
    "storage",
    "alarms",
    "scripting",
    "tabs"
  ],
  "host_permissions": [
    "https://*/*",
    "http://*/*"
  ],
  "background": {
    "service_worker": "background.js",
    "type": "module"
  },
  "action": {
    "default_popup": "popup/popup.html",
    "default_icon": {
      "16": "icons/icon16.png",
      "48": "icons/icon48.png",
      "128": "icons/icon128.png"
    }
  },
  "content_scripts": [
    {
      "matches": ["<all_urls>"],
      "js": ["content/scraper.js"],
      "run_at": "document_idle"
    }
  ],
  "icons": {
    "16": "icons/icon16.png",
    "48": "icons/icon48.png",
    "128": "icons/icon128.png"
  }
}
```

**Tasks**:
1. Create `manifest.json` with above configuration
2. Create placeholder icon files (or generate with icon generator)
3. Create basic file structure

---

### Phase 2: Queue Management System

**File**: `lib/queue-manager.js`

```javascript
/**
 * Queue Manager for sequential URL processing
 */
class QueueManager {
  constructor() {
    this.storageKey = 'scraper_queue';
    this.stateKey = 'scraper_state';
  }

  async getQueue() {
    const result = await chrome.storage.local.get(this.storageKey);
    return result[this.storageKey] || [];
  }

  async setQueue(queue) {
    await chrome.storage.local.set({ [this.storageKey]: queue });
  }

  async addURL(url, options = {}) {
    const queue = await this.getQueue();
    queue.push({
      id: Date.now().toString() + Math.random(),
      url,
      status: 'pending', // pending, processing, completed, failed
      timestamp: new Date().toISOString(),
      error: null,
      ...options
    });
    await this.setQueue(queue);
    return queue;
  }

  async removeURL(id) {
    const queue = await this.getQueue();
    const filtered = queue.filter(item => item.id !== id);
    await this.setQueue(filtered);
  }

  async getNextPending() {
    const queue = await this.getQueue();
    return queue.find(item => item.status === 'pending');
  }

  async updateStatus(id, status, data = {}) {
    const queue = await this.getQueue();
    const index = queue.findIndex(item => item.id === id);
    if (index !== -1) {
      queue[index] = { ...queue[index], status, ...data };
      await this.setQueue(queue);
    }
  }

  async clear() {
    await chrome.storage.local.set({ [this.storageKey]: [] });
  }

  async getState() {
    const result = await chrome.storage.local.get(this.stateKey);
    return result[this.stateKey] || { isRunning: false, currentIndex: 0 };
  }

  async setState(state) {
    await chrome.storage.local.set({ [this.stateKey]: state });
  }
}

export default QueueManager;
```

**Tasks**:
1. Implement QueueManager class
2. Add methods for CRUD operations on URL queue
3. Add state tracking (running/stopped, current index)
4. Test storage operations

---

### Phase 3: Service Worker (Background Script)

**File**: `background.js`

```javascript
import QueueManager from './lib/queue-manager.js';
import FileHandler from './lib/file-handler.js';

const queueManager = new QueueManager();
const fileHandler = new FileHandler();
let directoryHandle = null;

// Listen for messages from popup
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  switch (message.action) {
    case 'addToQueue':
      handleAddToQueue(message.urls);
      sendResponse({ success: true });
      break;
    case 'startScraping':
      handleStartScraping(message.directoryHandle);
      sendResponse({ success: true });
      break;
    case 'stopScraping':
      handleStopScraping();
      sendResponse({ success: true });
      break;
    case 'getQueue':
      getQueue().then(sendResponse);
      return true; // Async response
    case 'clearQueue':
      handleClearQueue();
      sendResponse({ success: true });
      break;
  }
});

// Handle adding URLs to queue
async function handleAddToQueue(urls) {
  for (const url of urls) {
    await queueManager.addURL(url);
  }
}

// Handle starting the scraping process
async function handleStartScraping(dirHandle) {
  directoryHandle = dirHandle;
  await queueManager.setState({ isRunning: true, currentIndex: 0 });

  // Set up alarm to keep service worker alive
  chrome.alarms.create('keepAlive', { periodInMinutes: 1 });

  // Process first item
  processNextItem();
}

// Handle stopping the scraping process
async function handleStopScraping() {
  await queueManager.setState({ isRunning: false });
  chrome.alarms.clear('keepAlive');
}

// Handle clearing the queue
async function handleClearQueue() {
  await handleStopScraping();
  await queueManager.clear();
}

// Process next item in queue
async function processNextItem() {
  const state = await queueManager.getState();
  if (!state.isRunning) {
    return;
  }

  const nextItem = await queueManager.getNextPending();
  if (!nextItem) {
    // All done
    await handleStopScraping();
    chrome.notifications.create({
      type: 'basic',
      iconUrl: 'icons/icon128.png',
      title: 'Scraping Complete',
      message: 'All URLs have been processed!'
    });
    return;
  }

  // Update status to processing
  await queueManager.updateStatus(nextItem.id, 'processing');

  // Open tab and inject scraper
  try {
    const tab = await chrome.tabs.create({ url: nextItem.url, active: false });

    // Wait for tab to load
    chrome.tabs.onUpdated.addListener(function listener(tabId, changeInfo) {
      if (tabId === tab.id && changeInfo.status === 'complete') {
        chrome.tabs.onUpdated.removeListener(listener);

        // Inject scraper after a delay for dynamic content
        setTimeout(async () => {
          try {
            const results = await chrome.tabs.sendMessage(tab.id, {
              action: 'scrape',
              url: nextItem.url
            });

            // Save results
            await saveResults(nextItem, results);

            // Close tab
            await chrome.tabs.remove(tab.id);

            // Update status
            await queueManager.updateStatus(nextItem.id, 'completed', {
              completedAt: new Date().toISOString()
            });

            // Process next
            await processNextItem();
          } catch (error) {
            await handleError(nextItem, tab.id, error);
          }
        }, 3000); // 3 second delay for JS rendering
      }
    });
  } catch (error) {
    await handleError(nextItem, null, error);
    await processNextItem();
  }
}

// Save results to file
async function saveResults(item, data) {
  if (!directoryHandle) {
    throw new Error('No directory selected');
  }

  const filename = generateFilename(item.url);
  await fileHandler.saveJSON(directoryHandle, filename, data);
}

// Handle errors
async function handleError(item, tabId, error) {
  console.error('Error processing URL:', item.url, error);

  if (tabId) {
    await chrome.tabs.remove(tabId);
  }

  await queueManager.updateStatus(item.id, 'failed', {
    error: error.message,
    failedAt: new Date().toISOString()
  });

  await processNextItem();
}

// Generate filename from URL
function generateFilename(url) {
  try {
    const urlObj = new URL(url);
    const hostname = urlObj.hostname.replace(/[^a-z0-9]/gi, '_');
    const pathname = urlObj.pathname.replace(/[^a-z0-9]/gi, '_').slice(0, 50);
    const timestamp = Date.now();
    return `${hostname}${pathname}_${timestamp}.json`;
  } catch {
    return `scraped_${Date.now()}.json`;
  }
}

// Keep-alive alarm handler
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === 'keepAlive') {
    // Keep service worker alive
    console.log('Keep alive ping');
  }
});

// Handle extension startup
chrome.runtime.onStartup.addListener(async () => {
  const state = await queueManager.getState();
  if (state.isRunning) {
    // Resume processing
    processNextItem();
  }
});
```

**Tasks**:
1. Implement main service worker logic
2. Set up message passing with popup
3. Implement sequential URL processing with tab management
4. Add error handling and recovery
5. Implement keep-alive mechanism with alarms

---

### Phase 4: File Handler (File System Access API)

**File**: `lib/file-handler.js`

```javascript
/**
 * File Handler for File System Access API
 */
class FileHandler {
  /**
   * Request directory access from user
   */
  async requestDirectoryAccess() {
    try {
      const handle = await window.showDirectoryPicker({
        mode: 'readwrite',
        startIn: 'documents'
      });
      return handle;
    } catch (error) {
      if (error.name === 'AbortError') {
        throw new Error('Directory selection was cancelled');
      }
      throw error;
    }
  }

  /**
   * Save JSON data to file in directory
   */
  async saveJSON(directoryHandle, filename, data) {
    try {
      // Get or create file handle
      const fileHandle = await directoryHandle.getFileHandle(filename, {
        create: true
      });

      // Create writable stream
      const writable = await fileHandle.createWritable();

      // Write JSON data
      await writable.write(JSON.stringify(data, null, 2));

      // Close stream
      await writable.close();

      return { success: true, filename };
    } catch (error) {
      throw new Error(`Failed to save file: ${error.message}`);
    }
  }

  /**
   * Save multiple JSON files
   */
  async saveMultipleJSON(directoryHandle, files) {
    const results = [];
    for (const file of files) {
      const result = await this.saveJSON(
        directoryHandle,
        file.filename,
        file.data
      );
      results.push(result);
    }
    return results;
  }

  /**
   * Verify directory access
   */
  async verifyPermission(directoryHandle) {
    const options = { mode: 'readwrite' };
    if ((await directoryHandle.queryPermission(options)) === 'granted') {
      return true;
    }
    if ((await directoryHandle.requestPermission(options)) === 'granted') {
      return true;
    }
    return false;
  }
}

export default FileHandler;
```

**Tasks**:
1. Implement FileHandler class with File System Access API
2. Add directory picker functionality
3. Implement JSON file saving
4. Add permission verification

---

### Phase 5: Content Script (Scraper)

**File**: `content/scraper.js`

```javascript
/**
 * Content Script - Runs in the context of web pages
 * Extracts data from dynamically rendered content
 */

// Listen for messages from background script
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.action === 'scrape') {
    const data = scrapePage(message.url);
    sendResponse(data);
  }
  return true;
});

/**
 * Main scraping function - customize this for your needs
 */
function scrapePage(url) {
  const scrapedData = {
    metadata: {
      url: url,
      timestamp: new Date().toISOString(),
      title: document.title
    },
    data: {}
  };

  // Extract common elements
  scrapedData.data = {
    // Basic page info
    title: document.title,
    metaDescription: getMetaDescription(),
    headings: extractHeadings(),
    paragraphs: extractParagraphs(),
    links: extractLinks(),
    images: extractImages(),

    // Custom selectors - ADD YOUR SPECIFIC SELECTORS HERE
    // Example:
    // products: extractProducts(),
    // articles: extractArticles(),
  };

  return scrapedData;
}

/**
 * Helper functions for data extraction
 */

function getMetaDescription() {
  const meta = document.querySelector('meta[name="description"]');
  return meta ? meta.content : '';
}

function extractHeadings() {
  const headings = [];
  document.querySelectorAll('h1, h2, h3, h4, h5, h6').forEach(h => {
    headings.push({
      tag: h.tagName,
      text: h.textContent.trim()
    });
  });
  return headings;
}

function extractParagraphs() {
  const paragraphs = [];
  document.querySelectorAll('p').forEach(p => {
    const text = p.textContent.trim();
    if (text.length > 0) {
      paragraphs.push(text);
    }
  });
  return paragraphs;
}

function extractLinks() {
  const links = [];
  document.querySelectorAll('a[href]').forEach(a => {
    links.push({
      text: a.textContent.trim(),
      href: a.href
    });
  });
  return links.slice(0, 100); // Limit to 100 links
}

function extractImages() {
  const images = [];
  document.querySelectorAll('img[src]').forEach(img => {
    images.push({
      src: img.src,
      alt: img.alt || ''
    });
  });
  return images.slice(0, 50); // Limit to 50 images
}

/**
 * Custom extraction examples - UNCOMMENT AND CUSTOMIZE
 */

/*
function extractProducts() {
  const products = [];
  document.querySelectorAll('.product-card').forEach(card => {
    products.push({
      name: card.querySelector('.product-name')?.textContent.trim(),
      price: card.querySelector('.price')?.textContent.trim(),
      description: card.querySelector('.description')?.textContent.trim()
    });
  });
  return products;
}

function extractArticles() {
  const articles = [];
  document.querySelectorAll('article').forEach(article => {
    articles.push({
      title: article.querySelector('h1, h2')?.textContent.trim(),
      author: article.querySelector('.author')?.textContent.trim(),
      date: article.querySelector('time')?.getAttribute('datetime'),
      content: article.querySelector('.content')?.textContent.trim()
    });
  });
  return articles;
}
*/
```

**File**: `content/selectors.js`

```javascript
/**
 * CSS Selectors for specific websites
 * Customize this file for your target websites
 */

export const SITE_SELECTORS = {
  'example.com': {
    title: 'h1.product-title',
    price: '.price-value',
    description: '.product-description'
  },

  'another-site.com': {
    title: '.article-title',
    author: '.author-name',
    content: '.article-body'
  }
};

export function getSelectorsForDomain(domain) {
  return SITE_SELECTORS[domain] || null;
}
```

**Tasks**:
1. Implement basic content script with message listener
2. Add generic data extraction functions
3. Create customizable selector system
4. Add custom extraction functions for specific sites

---

### Phase 6: Popup UI

**File**: `popup/popup.html`

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Web Scraper</title>
  <link rel="stylesheet" href="popup.css">
</head>
<body>
  <div class="container">
    <header>
      <h1>🕷️ Web Scraper</h1>
    </header>

    <section class="add-urls">
      <h2>Add URLs</h2>
      <textarea
        id="urlInput"
        placeholder="Enter URLs (one per line)&#10;https://example.com/page1&#10;https://example.com/page2"
        rows="4"
      ></textarea>
      <button id="addBtn" class="btn-primary">Add to Queue</button>
    </section>

    <section class="queue-section">
      <div class="queue-header">
        <h2>Queue (<span id="queueCount">0</span>)</h2>
        <button id="clearBtn" class="btn-secondary">Clear</button>
      </div>
      <div id="queueList" class="queue-list"></div>
    </section>

    <section class="controls">
      <button id="selectFolderBtn" class="btn-secondary">📁 Select Output Folder</button>
      <p id="folderStatus" class="status-text">No folder selected</p>
      <button id="startBtn" class="btn-primary" disabled>▶ Start Scraping</button>
      <button id="stopBtn" class="btn-danger" style="display: none;">⏹ Stop</button>
    </section>

    <section class="progress">
      <div class="progress-bar">
        <div id="progressFill" class="progress-fill"></div>
      </div>
      <p id="progressText" class="status-text">Ready</p>
    </section>

    <section class="stats">
      <div class="stat">
        <span class="stat-label">Completed:</span>
        <span id="completedCount" class="stat-value">0</span>
      </div>
      <div class="stat">
        <span class="stat-label">Failed:</span>
        <span id="failedCount" class="stat-value">0</span>
      </div>
    </section>
  </div>

  <script src="popup.js"></script>
</body>
</html>
```

**File**: `popup/popup.css`

```css
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  width: 400px;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  font-size: 14px;
  color: #333;
}

.container {
  padding: 16px;
}

header {
  margin-bottom: 16px;
}

header h1 {
  font-size: 20px;
  color: #1a1a1a;
}

section {
  margin-bottom: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid #e0e0e0;
}

section:last-child {
  border-bottom: none;
}

h2 {
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 8px;
  color: #666;
}

textarea {
  width: 100%;
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 4px;
  font-family: monospace;
  font-size: 12px;
  resize: vertical;
}

button {
  padding: 8px 16px;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
  transition: background-color 0.2s;
}

.btn-primary {
  background-color: #0066cc;
  color: white;
  width: 100%;
}

.btn-primary:hover:not(:disabled) {
  background-color: #0052a3;
}

.btn-primary:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}

.btn-secondary {
  background-color: #f0f0f0;
  color: #333;
}

.btn-secondary:hover {
  background-color: #e0e0e0;
}

.btn-danger {
  background-color: #dc3545;
  color: white;
  width: 100%;
}

.btn-danger:hover {
  background-color: #c82333;
}

.queue-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.queue-header button {
  padding: 4px 8px;
  font-size: 12px;
}

.queue-list {
  max-height: 150px;
  overflow-y: auto;
  border: 1px solid #e0e0e0;
  border-radius: 4px;
}

.queue-item {
  padding: 8px;
  border-bottom: 1px solid #f0f0f0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.queue-item:last-child {
  border-bottom: none;
}

.queue-item-url {
  flex: 1;
  font-size: 12px;
  word-break: break-all;
}

.queue-item-status {
  font-size: 10px;
  padding: 2px 6px;
  border-radius: 3px;
  font-weight: 600;
}

.status-pending {
  background-color: #fff3cd;
  color: #856404;
}

.status-processing {
  background-color: #cce5ff;
  color: #004085;
}

.status-completed {
  background-color: #d4edda;
  color: #155724;
}

.status-failed {
  background-color: #f8d7da;
  color: #721c24;
}

.controls {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.status-text {
  font-size: 12px;
  color: #666;
  text-align: center;
}

.progress-bar {
  height: 8px;
  background-color: #e0e0e0;
  border-radius: 4px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  background-color: #28a745;
  width: 0%;
  transition: width 0.3s;
}

.stats {
  display: flex;
  justify-content: space-around;
}

.stat {
  text-align: center;
}

.stat-label {
  font-size: 12px;
  color: #666;
}

.stat-value {
  font-size: 18px;
  font-weight: 600;
  color: #333;
}
```

**File**: `popup/popup.js`

```javascript
// DOM Elements
const urlInput = document.getElementById('urlInput');
const addBtn = document.getElementById('addBtn');
const clearBtn = document.getElementById('clearBtn');
const queueList = document.getElementById('queueList');
const queueCount = document.getElementById('queueCount');
const selectFolderBtn = document.getElementById('selectFolderBtn');
const folderStatus = document.getElementById('folderStatus');
const startBtn = document.getElementById('startBtn');
const stopBtn = document.getElementById('stopBtn');
const progressFill = document.getElementById('progressFill');
const progressText = document.getElementById('progressText');
const completedCount = document.getElementById('completedCount');
const failedCount = document.getElementById('failedCount');

// State
let directoryHandle = null;
let queue = [];
let state = { isRunning: false };

// Initialize
async function init() {
  await loadQueue();
  await loadState();
  renderQueue();
  updateUI();

  // Set up periodic refresh
  setInterval(async () => {
    await loadQueue();
    await loadState();
    renderQueue();
    updateUI();
  }, 1000);
}

// Load queue from storage
async function loadQueue() {
  const response = await chrome.runtime.sendMessage({ action: 'getQueue' });
  queue = response || [];
}

// Load state from storage
async function loadState() {
  const result = await chrome.storage.local.get('scraper_state');
  state = result.scraper_state || { isRunning: false };
}

// Save directory handle (using IndexedDB for persistent handles)
async function saveDirectoryHandle(handle) {
  // For simplicity, we'll just keep it in memory
  // In production, you'd use IndexedDB to persist across sessions
  directoryHandle = handle;
}

// Add URLs to queue
async function addURLs() {
  const text = urlInput.value.trim();
  if (!text) return;

  const urls = text.split('\n')
    .map(line => line.trim())
    .filter(line => line.length > 0)
    .map(line => {
      try {
        new URL(line);
        return line;
      } catch {
        return null;
      }
    })
    .filter(url => url !== null);

  if (urls.length === 0) {
    alert('Please enter valid URLs');
    return;
  }

  await chrome.runtime.sendMessage({ action: 'addToQueue', urls });
  urlInput.value = '';
  await loadQueue();
  renderQueue();
  updateUI();
}

// Clear queue
async function clearQueue() {
  if (confirm('Clear all URLs from queue?')) {
    await chrome.runtime.sendMessage({ action: 'clearQueue' });
    await loadQueue();
    renderQueue();
    updateUI();
  }
}

// Select output folder
async function selectFolder() {
  try {
    // This needs to be done from the service worker
    // For now, we'll trigger it via message
    const handle = await window.showDirectoryPicker({
      mode: 'readwrite',
      startIn: 'documents'
    });

    await saveDirectoryHandle(handle);
    folderStatus.textContent = `✓ ${handle.name}`;
    startBtn.disabled = false;
  } catch (error) {
    if (error.name !== 'AbortError') {
      console.error('Error selecting folder:', error);
    }
  }
}

// Start scraping
async function startScraping() {
  if (!directoryHandle) {
    alert('Please select an output folder first');
    return;
  }

  startBtn.style.display = 'none';
  stopBtn.style.display = 'block';

  await chrome.runtime.sendMessage({
    action: 'startScraping',
    directoryHandle: directoryHandle
  });
}

// Stop scraping
async function stopScraping() {
  await chrome.runtime.sendMessage({ action: 'stopScraping' });
}

// Render queue
function renderQueue() {
  queueCount.textContent = queue.length;
  queueList.innerHTML = '';

  queue.forEach(item => {
    const div = document.createElement('div');
    div.className = 'queue-item';

    const url = document.createElement('div');
    url.className = 'queue-item-url';
    url.textContent = item.url;
    url.title = item.url;

    const status = document.createElement('span');
    status.className = `queue-item-status status-${item.status}`;
    status.textContent = item.status;

    div.appendChild(url);
    div.appendChild(status);
    queueList.appendChild(div);
  });
}

// Update UI based on state
function updateUI() {
  const completed = queue.filter(item => item.status === 'completed').length;
  const failed = queue.filter(item => item.status === 'failed').length;
  const total = queue.length;

  completedCount.textContent = completed;
  failedCount.textContent = failed;

  const progress = total > 0 ? ((completed + failed) / total) * 100 : 0;
  progressFill.style.width = `${progress}%`;

  if (state.isRunning) {
    startBtn.style.display = 'none';
    stopBtn.style.display = 'block';
    progressText.textContent = 'Processing...';
  } else {
    startBtn.style.display = 'block';
    stopBtn.style.display = 'none';
    startBtn.disabled = !directoryHandle || queue.length === 0;

    if (completed + failed === total && total > 0) {
      progressText.textContent = 'Complete!';
    } else {
      progressText.textContent = 'Ready';
    }
  }
}

// Event listeners
addBtn.addEventListener('click', addURLs);
clearBtn.addEventListener('click', clearQueue);
selectFolderBtn.addEventListener('click', selectFolder);
startBtn.addEventListener('click', startScraping);
stopBtn.addEventListener('click', stopScraping);

// Start
init();
```

**Tasks**:
1. Create popup UI with HTML/CSS
2. Implement queue display and management
3. Add folder selection functionality
4. Add start/stop controls
5. Implement progress tracking
6. Connect to background script via message passing

---

### Phase 7: Testing & Debugging

**Testing Checklist**:

1. **Unit Testing**:
   - Test QueueManager operations
   - Test FileHandler operations
   - Test data parsing functions

2. **Integration Testing**:
   - Test adding URLs to queue
   - Test sequential processing
   - Test file saving
   - Test error handling

3. **Manual Testing**:
   - Load extension in Edge
   - Add sample URLs
   - Select output folder
   - Run scraping
   - Verify output files

**Debugging Tools**:
- Chrome Extensions page: `edge://extensions`
- Service Worker logs: Click "service worker" link in extensions page
- Popup debug: Right-click popup → Inspect
- Content script debug: DevTools on scraped pages

**Common Issues & Solutions**:

| Issue | Solution |
|-------|----------|
| Service worker dies after 30s | Use `chrome.alarms` for keep-alive |
| Can't access local files | Use File System Access API |
| Dynamic content not loaded | Increase delay in `processNextItem()` |
| CORS errors | Add URLs to `host_permissions` |
| Memory issues with many tabs | Process sequentially (already implemented) |

---

### Phase 8: Deployment

**Steps**:

1. **Package Extension**:
   - Open `edge://extensions/`
   - Enable "Developer mode"
   - Click "Load unpacked"
   - Select extension folder

2. **Testing**:
   - Test with 5-10 URLs first
   - Verify JSON output format
   - Check error handling

3. **Publish to Edge Add-ons** (optional):
   - Create ZIP of extension folder
   - Submit to [Microsoft Edge Add-ons](https://partner.microsoft.com/dashboard)
   - Go through review process

---

## Customization Guide

### Adding Custom Scraping Logic

1. **Open** `content/scraper.js`

2. **Add custom extraction function**:
```javascript
function extractProducts() {
  const products = [];
  document.querySelectorAll('.your-product-selector').forEach(el => {
    products.push({
      name: el.querySelector('.name')?.textContent.trim(),
      price: el.querySelector('.price')?.textContent.trim(),
      // Add more fields as needed
    });
  });
  return products;
}
```

3. **Update `scrapePage()` function**:
```javascript
function scrapePage(url) {
  const scrapedData = {
    metadata: {
      url: url,
      timestamp: new Date().toISOString(),
      title: document.title
    },
    data: {
      products: extractProducts(),  // Add your custom function
      // ... other fields
    }
  };
  return scrapedData;
}
```

### Adding Site-Specific Selectors

1. **Open** `content/selectors.js`

2. **Add site configuration**:
```javascript
export const SITE_SELECTORS = {
  'your-site.com': {
    title: 'h1.product-title',
    price: '.price-value',
    // Add more selectors
  },
  // ... more sites
};
```

3. **Update scraper to use site-specific selectors** (if needed)

---

## Estimated Development Time

| Phase | Tasks | Time |
|-------|-------|------|
| 1. Project Setup | Manifest, file structure | 1-2 hours |
| 2. Queue System | Storage, CRUD operations | 2-3 hours |
| 3. Service Worker | Main orchestration logic | 4-6 hours |
| 4. File Handler | File System Access API | 2-3 hours |
| 5. Content Script | Scraping logic | 3-5 hours |
| 6. Popup UI | Interface and controls | 4-6 hours |
| 7. Testing | Debug and fix issues | 3-5 hours |
| 8. Deployment | Package and publish | 1-2 hours |
| **Total** | | **20-32 hours** |

---

## ⚠️ IMPORTANT: Anti-Bot Detection & Ozon Scraping

### Why Playwright/Puppeteer Get Blocked by Ozon

**Ozon uses sophisticated anti-bot systems** that detect headless browsers through multiple methods:

| Detection Method | What They Check | Playwright/Puppeteer Vulnerability |
|------------------|-----------------|------------------------------------|
| **navigator.webdriver** | `true` in headless browsers | ❌ Exposed by default |
| **Headless indicators** | Missing browser features, inconsistent fonts | ❌ Easily detected |
| **Browser fingerprint** | Canvas, WebGL, audio fingerprint | ❌ Different from real browsers |
| **Behavioral patterns** | Mouse movement, scroll timing, page interaction | ❌ No human-like behavior |
| **CDP detection** | Chrome DevTools Protocol signatures | ❌ Actively used by these tools |
| **Timing patterns** | Too-fast page loads, instant actions | ❌ Dead giveaway |
| **User-Agent inconsistency** | Headless UA with normal browser features | ❌ Mismatch detected |

**Result**: Ozon shows "We've detected suspicious activity" or CAPTCHA.

---

### Browser Extension Approach: **MUCH BETTER** ✅

Using a browser extension in a **normal, user-controlled browser** provides significant advantages:

#### ✅ **Advantages of Browser Extension**

| Aspect | Extension | Headless (Playwright) |
|--------|-----------|----------------------|
| **navigator.webdriver** | `false` (normal browser) | `true` (easily detected) |
| **Browser fingerprint** | Natural, consistent | Synthetic, detectable |
| **Browser history** | Real user history | Empty/new profile |
| **Cookies** | Persistent session | Fresh session |
| **User behavior** | Can simulate human actions | Harder to mimic naturally |
| **IP reputation** | Your real IP (good standing) | May need proxy rotation |
| **JavaScript execution** | Native browser engine | Same, but flagged |
| **Detection risk** | **LOW** | **HIGH** |

**Key Insight**: Extension runs in your real browser with your real cookies, history, and session - Ozon sees you as a legitimate user!

---

### ⚡ Enhanced Anti-Detection for Ozon

To maximize success with Ozon, add these features to the extension:

#### 1. **Human-like Delays** (CRITICAL)

Update `background.js` processNextItem():

```javascript
// Add random delay between pages (5-15 seconds)
const MIN_DELAY = 5000;
const MAX_DELAY = 15000;

function getRandomDelay(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

// In processNextItem(), after scraping:
const delay = getRandomDelay(MIN_DELAY, MAX_DELAY);
await new Promise(resolve => setTimeout(resolve, delay));

// Then process next item
await processNextItem();
```

#### 2. **Scroll & Mouse Movement Simulation**

Add to `content/scraper.js`:

```javascript
/**
 * Simulate human-like scrolling
 */
async function humanScroll() {
  const scrollHeight = document.body.scrollHeight;
  const viewportHeight = window.innerHeight;
  const steps = Math.ceil(scrollHeight / viewportHeight);

  for (let i = 0; i < steps; i++) {
    window.scrollTo({
      top: i * viewportHeight,
      behavior: 'smooth'
    });
    await new Promise(r => setTimeout(r, getRandomDelay(500, 1500)));
  }

  // Scroll back to top
  window.scrollTo({ top: 0, behavior: 'smooth' });
}

function getRandomDelay(min, max) {
  return Math.floor(Math.random() * (max - min + 1)) + min;
}

// Call before scraping
await humanScroll();
```

#### 3. **Wait for Specific Elements**

Don't use fixed timeouts - wait for actual content:

```javascript
/**
 * Wait for element to appear
 */
function waitForElement(selector, timeout = 10000) {
  return new Promise((resolve, reject) => {
    const element = document.querySelector(selector);
    if (element) {
      resolve(element);
      return;
    }

    const observer = new MutationObserver(() => {
      const el = document.querySelector(selector);
      if (el) {
        observer.disconnect();
        resolve(el);
      }
    });

    observer.observe(document.body, {
      childList: true,
      subtree: true
    });

    setTimeout(() => {
      observer.disconnect();
      reject(new Error(`Element ${selector} not found`));
    }, timeout);
  });
}

// Use in scraping
await waitForElement('.product-card, .item, [data-testid]');
```

#### 4. **Randomize Request Patterns**

Update manifest permissions and add:

```javascript
// Randomize order of URLs occasionally
async function shuffleQueue() {
  const queue = await queueManager.getQueue();
  const shuffled = [...queue].sort(() => Math.random() - 0.5);
  await queueManager.setQueue(shuffled);
}
```

#### 5. **Session Persistence**

- Keep browser logged into Ozon (manually log in once)
- Extension will use your existing session
- Don't clear cookies or localStorage

---

### 🛡️ Detection Risk Assessment

| Approach | Detection Risk | Success Rate with Ozon |
|----------|----------------|------------------------|
| **Plain Playwright** | 🔴 VERY HIGH | ~10-20% |
| **Playwright + Stealth** | 🟡 MEDIUM | ~40-60% |
| **Browser Extension** | 🟢 LOW | **~80-95%** |
| **Extension + Anti-Detection** | 🟢 VERY LOW | **~95%+** |

---

### 📋 Updated Recommendations for Ozon

#### **DO:**
✅ Use browser extension approach
✅ Add human-like delays (5-15s between pages)
✅ Simulate scrolling behavior
✅ Use real browser with login session
✅ Process sequentially (not parallel)
✅ Wait for elements, not fixed timeouts
✅ Randomize patterns occasionally

#### **DON'T:**
❌ Use headless Playwright/Puppeteer
❌ Process multiple pages simultaneously
❌ Make too many requests too quickly
❌ Use obvious delays (exactly 3 seconds every time)
❌ Clear cookies/session
❌ Use proxy services (unless needed)

---

### 🕵️ Honeypot Detection & Avoidance

**Honeypots** are hidden elements designed to trap bots. They're invisible to humans but detectable when scraping HTML/JS.

#### Common Honeypot Techniques

| Technique | How It Works | Detection Method |
|-----------|--------------|------------------|
| **CSS Hidden** | `display: none`, `visibility: hidden` | Check computed styles |
| **Off-screen** | `position: absolute; left: -9999px` | Check coordinates |
| **Zero Opacity** | `opacity: 0` | Check computed opacity |
| **Tiny Size** | `width: 0; height: 0` or `1x1` | Check dimensions |
| **Z-index Trap** | Hidden behind other elements | Check stacking context |
| **Comment Nodes** | Links in HTML comments | Ignore comments |
| **Suspicious Classes** | `.honeypot`, `.bot-trap` | Check class names |
| **Form Inputs** | Hidden fields that trigger on submit | Don't interact |

---

#### Honeypot Detection Module

Create `content/honeypot-detector.js`:

```javascript
/**
 * Honeypot Detection Module
 * Detects and avoids anti-bot traps
 */

class HoneypotDetector {
  constructor() {
    this.detectedHoneypots = [];
  }

  /**
   * Check if element is a honeypot
   */
  isHoneypot(element) {
    if (!element) return false;

    // 1. Check computed CSS styles
    const styles = window.getComputedStyle(element);

    // display: none or visibility: hidden
    if (styles.display === 'none' || styles.visibility === 'hidden') {
      return { isHoneypot: true, reason: 'CSS hidden (display/visibility)' };
    }

    // opacity: 0
    if (styles.opacity === '0' || styles.opacity === 0) {
      return { isHoneypot: true, reason: 'CSS opacity: 0' };
    }

    // 2. Check element position and size
    const rect = element.getBoundingClientRect();

    // Off-screen (negative coordinates or far outside viewport)
    if (rect.right < 0 || rect.bottom < 0 ||
        rect.left > window.innerWidth || rect.top > window.innerHeight) {
      return { isHoneypot: true, reason: 'Positioned off-screen' };
    }

    // Zero or tiny size
    if (rect.width === 0 || rect.height === 0 ||
        (rect.width === 1 && rect.height === 1)) {
      return { isHoneypot: true, reason: 'Zero or tiny size' };
    }

    // 3. Check suspicious class names
    const classes = element.className.toLowerCase();
    const suspiciousClasses = [
      'honeypot', 'bot-trap', 'spam-trap', 'hidden-field',
      'bot-check', 'robot-trap', 'anti-bot', 'detector'
    ];

    for (const suspicious of suspiciousClasses) {
      if (classes.includes(suspicious)) {
        return { isHoneypot: true, reason: `Suspicious class: ${suspicious}` };
      }
    }

    // 4. Check for aria-hidden attribute
    if (element.getAttribute('aria-hidden') === 'true') {
      // This might be legitimate, so use with caution
      // Some sites use aria-hidden for decorative elements
      // We'll flag it but not necessarily skip it
      return { isHoneypot: true, reason: 'aria-hidden="true"', severity: 'low' };
    }

    // 5. Check if hidden by parent
    let parent = element.parentElement;
    let depth = 0;
    while (parent && depth < 10) {
      const parentStyles = window.getComputedStyle(parent);
      if (parentStyles.display === 'none' || parentStyles.visibility === 'hidden') {
        return { isHoneypot: true, reason: 'Parent is hidden' };
      }
      parent = parent.parentElement;
      depth++;
    }

    // 6. Check for trap attributes (data-* attributes)
    const trapAttrs = ['data-honeypot', 'data-bot-trap', 'data-trap'];
    for (const attr of trapAttrs) {
      if (element.hasAttribute(attr)) {
        return { isHoneypot: true, reason: `Trap attribute: ${attr}` };
      }
    }

    // Not a honeypot
    return { isHoneypot: false };
  }

  /**
   * Filter out honeypots from element list
   */
  filterHoneypots(elements) {
    const valid = [];
    const honeypots = [];

    elements.forEach(el => {
      const result = this.isHoneypot(el);
      if (result.isHoneypot) {
        honeypots.push({ element: el, ...result });
        this.detectedHoneypots.push({ element: el, ...result });
      } else {
        valid.push(el);
      }
    });

    // Log honeypots for debugging
    if (honeypots.length > 0) {
      console.log(`🍯 Filtered ${honeypots.length} honeypot(s):`, honeypots);
    }

    return { valid, honeypots };
  }

  /**
   * Safe querySelectorAll that excludes honeypots
   */
  safeQuerySelectorAll(selector) {
    const elements = document.querySelectorAll(selector);
    const { valid } = this.filterHoneypots(Array.from(elements));
    return valid;
  }

  /**
   * Get report of detected honeypots
   */
  getHoneypotReport() {
    return {
      total: this.detectedHoneypots.length,
      traps: this.detectedHoneypots.map(h => ({
        reason: h.reason,
        tagName: h.element.tagName,
        className: h.element.className
      }))
    };
  }

  /**
   * Check for specific Ozon honeypot patterns
   */
  detectOzonHoneypots() {
    const traps = [];

    // Check for hidden product cards
    const hiddenProducts = document.querySelectorAll('[data-widget="webProductHeading"]');
    hiddenProducts.forEach(el => {
      const result = this.isHoneypot(el);
      if (result.isHoneypot) {
        traps.push({ type: 'product', ...result });
      }
    });

    // Check for hidden price elements
    const hiddenPrices = document.querySelectorAll('[data-widget="webPrice"]');
    hiddenPrices.forEach(el => {
      const result = this.isHoneypot(el);
      if (result.isHoneypot) {
        traps.push({ type: 'price', ...result });
      }
    });

    // Check for hidden links (common Ozon trap)
    const hiddenLinks = document.querySelectorAll('a[href]');
    hiddenLinks.forEach(el => {
      const result = this.isHoneypot(el);
      if (result.isHoneypot) {
        traps.push({ type: 'link', href: el.href, ...result });
      }
    });

    return traps;
  }
}

export default HoneypotDetector;
```

---

#### Integration with Scraper

Update `content/scraper.js`:

```javascript
import HoneypotDetector from './honeypot-detector.js';

// Initialize detector
const honeypotDetector = new HoneypotDetector();

// Listen for messages from background script
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.action === 'scrape') {
    const data = scrapePage(message.url);

    // Include honeypot report
    data.honeypotReport = honeypotDetector.getHoneypotReport();

    sendResponse(data);
  }
  return true;
});

/**
 * Extract products with honeypot filtering
 */
function extractProducts() {
  // Use safe querySelectorAll that filters honeypots
  const productCards = honeypotDetector.safeQuerySelectorAll('.product-card');

  const products = [];
  productCards.forEach(card => {
    // Double-check each card
    const check = honeypotDetector.isHoneypot(card);
    if (check.isHoneypot) {
      console.warn('Skipping honeypot product:', check);
      return; // Skip this card
    }

    products.push({
      name: card.querySelector('.product-name')?.textContent.trim(),
      price: card.querySelector('.price')?.textContent.trim(),
      // ... other fields
    });
  });

  return products;
}

/**
 * Extract links safely (common honeypot target)
 */
function extractLinks() {
  const allLinks = document.querySelectorAll('a[href]');
  const { valid, honeypots } = honeypotDetector.filterHoneypots(Array.from(allLinks));

  const links = valid.slice(0, 100).map(a => ({
    text: a.textContent.trim(),
    href: a.href
  }));

  // Warn if we found honeypot links
  if (honeypots.length > 0) {
    console.warn(`⚠️ Found and skipped ${honeypots.length} honeypot link(s)`);
  }

  return links;
}
```

---

#### Anti-Honeypot Best Practices

```javascript
/**
 * Safe scraping with honeypot avoidance
 */
async function safeScrapePage() {
  // 1. Check for honeypots before scraping
  const detector = new HoneypotDetector();
  const ozonTraps = detector.detectOzonHoneypots();

  if (ozonTraps.length > 0) {
    console.warn('🚨 Ozon honeypots detected:', ozonTraps);
    // Continue but be extra careful
  }

  // 2. Use safe selectors
  const elements = detector.safeQuerySelectorAll('.product-card');

  // 3. Verify each element before interaction
  const safeElements = elements.filter(el => {
    const check = detector.isHoneypot(el);
    if (check.isHoneypot) {
      console.warn('Skipping honeypot:', check.reason);
      return false;
    }
    return true;
  });

  // 4. Don't interact with suspicious elements
  // - No clicks on hidden elements
  // - No form submission of hidden fields
  // - No following hidden links

  // 5. Return data with honeypot report
  return {
    data: extractData(safeElements),
    honeypotReport: detector.getHoneypotReport(),
    timestamp: new Date().toISOString()
  };
}

/**
 * Initialize honeypot detector on page load
 */
(function initAntiHoneypot() {
  const detector = new HoneypotDetector();

  // Run detection on page load
  window.addEventListener('load', () => {
    setTimeout(() => {
      const traps = detector.detectOzonHoneypots();
      if (traps.length > 0) {
        console.log('🛡️ Honeypot protection active - detected traps:', traps.length);
      }
    }, 1000);
  });

  // Expose detector globally for debugging
  window.__honeypotDetector = detector;
})();
```

---

#### Advanced: Behavioral Honeypot Detection

Some honeypots trigger on specific behaviors:

```javascript
/**
 * Behavioral honeypot detector
 * Detects traps that trigger on interaction
 */
class BehavioralHoneypotDetector {
  /**
   * Check if clicking an element is safe
   */
  isSafeToClick(element) {
    // Don't click hidden elements
    if (this.isHidden(element)) {
      return false;
    }

    // Don't click elements with trap handlers
    const handlers = this.getEventHandlers(element);
    if (handlers.some(h => h.toString().includes('honeypot') ||
                           h.toString().includes('bot'))) {
      return false;
    }

    // Don't click links to suspicious destinations
    if (element.tagName === 'A') {
      const href = element.href;
      if (href.includes('trap') || href.includes('honeypot')) {
        return false;
      }
    }

    return true;
  }

  /**
   * Check if element is hidden
   */
  isHidden(element) {
    const styles = window.getComputedStyle(element);
    const rect = element.getBoundingClientRect();

    return styles.display === 'none' ||
           styles.visibility === 'hidden' ||
           styles.opacity === '0' ||
           rect.width === 0 ||
           rect.height === 0;
  }

  /**
   * Get event handlers (if accessible)
   */
  getEventHandlers(element) {
    // This is limited by browser security
    // Most event handlers are not accessible from content scripts
    return [];
  }
}
```

---

### Honeypot Detection Checklist

✅ **Implement These Checks:**
- [ ] CSS hidden detection (display, visibility, opacity)
- [ ] Off-screen element detection
- [ ] Zero/tiny size detection
- [ ] Suspicious class name detection
- [ ] aria-hidden detection
- [ ] Parent element inheritance check
- [ ] data-attribute trap detection
- [ ] Ozon-specific trap patterns
- [ ] Behavioral trap detection
- [ ] Honeypot reporting

⚠️ **Remember:**
- Some hidden elements are legitimate (aria-hidden for decorative elements)
- Log all detected honeypots for analysis
- Don't interact with ANY hidden element
- When in doubt, skip the element

---

### 🔧 Ozon-Specific Configuration

Update `content/scraper.js` with Ozon selectors:

```javascript
/**
 * Ozon-specific extraction
 */
function extractOzonProduct() {
  const product = {
    // Basic metadata
    url: window.location.href,
    title: document.title,

    // Ozon-specific selectors (2025)
    name: document.querySelector('[data-widget="webProductHeading"]')?.textContent?.trim(),
    price: document.querySelector('[data-widget="webPrice"]')?.textContent?.trim(),
    oldPrice: document.querySelector('.item-price--old')?.textContent?.trim(),
    rating: document.querySelector('.rating')?.textContent?.trim(),
    reviews: document.querySelector('[data-widget="webReviews"]')?.textContent?.trim(),

    // Images
    images: Array.from(document.querySelectorAll('.gallery img')).map(img => img.src),

    // Description
    description: document.querySelector('[data-widget="webDescription"]')?.textContent?.trim(),

    // Attributes
    attributes: {}
  };

  // Extract attributes table
  document.querySelectorAll('.attributes-item').forEach(item => {
    const key = item.querySelector('.attributes-key')?.textContent?.trim();
    const value = item.querySelector('.attributes-value')?.textContent?.trim();
    if (key && value) {
      product.attributes[key] = value;
    }
  });

  return product;
}

// In scrapePage():
if (window.location.hostname.includes('ozon.ru')) {
  scrapedData.data = extractOzonProduct();
}
```

---

### 📊 Success Strategy Summary

```
┌─────────────────────────────────────────────────────────────┐
│               OZON SCRAPING SUCCESS STRATEGY                 │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Use browser extension (NOT Playwright)                  │
│     ↓                                                        │
│  2. Manually log into Ozon in your browser                  │
│     ↓                                                        │
│  3. Add 5-15 second random delays between pages             │
│     ↓                                                        │
│  4. Simulate scrolling behavior on each page                │
│     ↓                                                        │
│  5. Process URLs sequentially (not parallel)                │
│     ↓                                                        │
│  6. Use specific Ozon selectors                              │
│     ↓                                                        │
│  ✅ SUCCESS: ~95% detection avoidance                        │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

---

## Resources for Anti-Detection

### Research & Tools
- [How to Bypass Bot Detection in 2025: 7 Proven Methods](https://www.scraperapi.com/web-scraping/how-to-bypass-bot-detection/) - Comprehensive anti-bot techniques
- [Headless vs. Headful Browsers in 2025](https://scrapingant.com/blog/headless-vs-headful-browsers-in-2025-detection-tradeoffs) - Detection tradeoffs analysis
- [Browser Fingerprinting Guide: Detection & Bypass Methods](https://www.browserless.io/blog/device-fingerprint) - Deep dive on fingerprinting
- [Web Scraping in 2025: Bypassing Modern Bot Detection](https://medium.com/@sohail_saifii/web-scraping-in-2025-bypassing-modern-bot-detection-fcab286b117d) - Modern detection systems

### Ozon-Specific
- [ozon-search-queries-collector](https://github.com/sergerdn/ozon-search-queries-collector) - Real-world Ozon scraping research

---

## Alternative Approach: Standalone Script

If you prefer a simpler approach without the browser extension:

### Using Puppeteer (Node.js)

```javascript
// scraper.js
const puppeteer = require('puppeteer');
const fs = require('fs').promises;
const path = require('path');

const URLs = [
  'https://example.com/page1',
  'https://example.com/page2',
  // Add more URLs
];

const OUTPUT_DIR = './output';

async function scrapePage(page, url) {
  await page.goto(url, { waitUntil: 'networkidle2' });

  // Wait for dynamic content
  await page.waitForTimeout(3000);

  const data = await page.evaluate(() => {
    return {
      title: document.title,
      // Add your custom extraction logic here
    };
  });

  return data;
}

async function main() {
  const browser = await puppeteer.launch({ headless: false });

  // Create output directory
  await fs.mkdir(OUTPUT_DIR, { recursive: true });

  for (let i = 0; i < URLs.length; i++) {
    const url = URLs[i];
    console.log(`Processing ${i + 1}/${URLs.length}: ${url}`);

    try {
      const page = await browser.newPage();
      const data = await scrapePage(page, url);

      // Save to file
      const filename = `scrape_${i + 1}_${Date.now()}.json`;
      const filepath = path.join(OUTPUT_DIR, filename);
      await fs.writeFile(filepath, JSON.stringify(data, null, 2));

      console.log(`✓ Saved: ${filename}`);
      await page.close();
    } catch (error) {
      console.error(`✗ Error: ${error.message}`);
    }
  }

  await browser.close();
  console.log('Done!');
}

main();
```

**Install dependencies**:
```bash
npm init -y
npm install puppeteer
```

**Run**:
```bash
node scraper.js
```

---

## Resources & References

### Official Documentation
- [Microsoft Edge Extensions Documentation](https://learn.microsoft.com/en-us/microsoft-edge/extensions/landing/)
- [Chrome Extension Manifest V3](https://developer.chrome.com/docs/extensions/mv3/)
- [Service Worker Lifecycle](https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle)
- [File System Access API](https://developer.chrome.com/docs/capabilities/web-apis/file-system-access)
- [MDN File System API](https://developer.mozilla.org/en-US/docs/Web/API/File_System_API)

### Tutorials & Guides
- [How to Make Your First Chrome Extension with Manifest V3](https://dev.to/otieno_keith/how-to-make-your-first-chrome-extension-with-manifest-v3-38do)
- [Building Persistent Chrome Extension using Manifest V3](https://rahulnegi20.medium.com/building-persistent-chrome-extension-using-manifest-v3-198000bf6db6)
- [What is Background Script / Service Worker](https://www.youtube.com/watch?v=kuKfv-M3KFk)

### Examples & Reference
- [Web Scraper Extension](https://webscraper.io/) - Study existing implementation
- [Agenty Advanced Web Scraper](https://chromewebstore.google.com/detail/agenty-advanced-web-scrap/gpolcofcjjiooogejfbaamdgmgfehgff)
- [Chrome Extensions Samples](https://github.com/GoogleChrome/chrome-extensions-samples)

---

## Summary

This plan provides a complete implementation for an MS Edge web scraper extension with:

✅ Sequential URL processing (one at a time)
✅ Dynamic JavaScript content support (via content scripts)
✅ JSON file output (via File System Access API)
✅ User-friendly UI for queue management
✅ Progress tracking and error handling
✅ Manifest V3 compliant

**Next Steps**:
1. Create the project folder structure
2. Implement each phase sequentially
3. Test thoroughly with sample URLs
4. Customize scraping logic for your specific needs
5. Deploy and use!
