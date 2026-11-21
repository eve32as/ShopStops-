// Deterministic simulated foreground GPS; never calls browser/device geolocation.
type Position = { timestamp: number; coords: { latitude: number; longitude: number } };
let position: Position = { timestamp: Date.UTC(2026, 10, 18, 12), coords: { latitude: 38.91, longitude: -76.98 } };
let permission = 'granted';
const watchers = new Set<(value: Position) => void>();
let permissionRequests = 0;
let removed = 0;
export const Accuracy = { Balanced: 3 };
export async function requestForegroundPermissionsAsync() { permissionRequests++; return { status: permission }; }
export async function getCurrentPositionAsync() { return position; }
export async function watchPositionAsync(_options: unknown, receive: (value: Position) => void) {
  watchers.add(receive);
  return { remove() { if (watchers.delete(receive)) removed++; } };
}
export const locationBridge = {
  permission(value: string) { permission = value; },
  emit(value: Position) { position = value; watchers.forEach(receive => receive(value)); },
  stats() { return { watchers: watchers.size, removed, permissionRequests }; },
};