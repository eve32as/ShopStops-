import * as Location from "expo-location";
import * as SecureStore from "expo-secure-store";
import * as TaskManager from "expo-task-manager";
import { ApiError, apiRequest, type ApiSettings } from "./api";

export const DRIVER_LOCATION_TASK = "shopstops-driver-location";

const contextKey = "shopstops.driver-location.context";
const lastSampleKey = "shopstops.driver-location.last-sample";

type LocationTaskData = {
  locations?: Location.LocationObject[];
};

type BackgroundContext = {
  routeId: string;
  settings: ApiSettings;
};

async function clearBackgroundContext() {
  await Promise.all([
    SecureStore.deleteItemAsync(contextKey),
    SecureStore.deleteItemAsync(lastSampleKey),
  ]);
}

async function stopTaskAndClearContext() {
  try {
    if (await Location.hasStartedLocationUpdatesAsync(DRIVER_LOCATION_TASK)) {
      await Location.stopLocationUpdatesAsync(DRIVER_LOCATION_TASK);
    }
  } finally {
    await clearBackgroundContext();
  }
}

if (!TaskManager.isTaskDefined(DRIVER_LOCATION_TASK)) {
  TaskManager.defineTask<LocationTaskData>(DRIVER_LOCATION_TASK, async ({ data, error }) => {
    try {
      if (error) {
        await stopTaskAndClearContext();
        return;
      }
      if (!data?.locations?.length) return;

      const storedContext = await SecureStore.getItemAsync(contextKey);
      if (!storedContext) {
        await stopTaskAndClearContext();
        return;
      }

      let context: BackgroundContext;
      try {
        context = JSON.parse(storedContext) as BackgroundContext;
      } catch {
        await stopTaskAndClearContext();
        return;
      }
      if (
        !context ||
        typeof context.routeId !== "string" ||
        !context.routeId ||
        !context.settings?.baseUrl ||
        !context.settings?.sessionToken
      ) {
        await stopTaskAndClearContext();
        return;
      }

      let lastSampleAt = Number(await SecureStore.getItemAsync(lastSampleKey)) || 0;
      const locations = data.locations.slice().sort((a, b) => a.timestamp - b.timestamp);
      for (const location of locations) {
        const { latitude, longitude } = location.coords;
        if (
          !Number.isFinite(location.timestamp) ||
          location.timestamp < lastSampleAt + 5000 ||
          location.timestamp > Date.now() + 60_000 ||
          !Number.isFinite(latitude) ||
          latitude < -90 ||
          latitude > 90 ||
          !Number.isFinite(longitude) ||
          longitude < -180 ||
          longitude > 180
        ) {
          continue;
        }

        try {
          await apiRequest(
            context.settings,
            `driver/routes/${encodeURIComponent(context.routeId)}/location`,
            {
              method: "POST",
              body: JSON.stringify({
                eventId: `gps-${context.routeId}-${Math.trunc(location.timestamp)}`,
                observedAt: new Date(location.timestamp).toISOString(),
                location: { latitude, longitude },
              }),
            },
          );
          lastSampleAt = location.timestamp;
          await SecureStore.setItemAsync(lastSampleKey, String(lastSampleAt), {
            keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK,
          });
        } catch (requestError) {
          if (
            requestError instanceof ApiError &&
            [401, 403, 404, 409].includes(requestError.status ?? 0)
          ) {
            await stopTaskAndClearContext();
          }
          return;
        }
      }
    } catch {
      return;
    }
  });
}

export async function startDriverLocationUpdates(routeId: string, settings: ApiSettings) {
  if (!routeId.trim() || !settings.baseUrl.trim() || !settings.sessionToken.trim()) {
    throw new Error("An active route and signed-in gateway session are required.");
  }
  await SecureStore.setItemAsync(contextKey, JSON.stringify({ routeId, settings }), {
    keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK,
  });
  await SecureStore.deleteItemAsync(lastSampleKey);
  try {
    await Location.startLocationUpdatesAsync(DRIVER_LOCATION_TASK, {
      accuracy: Location.Accuracy.Balanced,
      timeInterval: 5000,
      distanceInterval: 0,
      pausesUpdatesAutomatically: false,
      showsBackgroundLocationIndicator: true,
      foregroundService: {
        notificationTitle: "ShopStops driver tracking",
        notificationBody: "Location sharing is active for your assigned delivery route.",
        killServiceOnDestroy: false,
      },
    });
  } catch (error) {
    await clearBackgroundContext();
    throw error;
  }
}

export async function stopDriverLocationUpdates() {
  await stopTaskAndClearContext();
}

export async function isDriverLocationUpdatesActive() {
  try {
    return await Location.hasStartedLocationUpdatesAsync(DRIVER_LOCATION_TASK);
  } catch {
    return false;
  }
}

export async function getDriverLocationContext(): Promise<BackgroundContext | null> {
  try {
    if (!(await isDriverLocationUpdatesActive())) return null;
    const storedContext = await SecureStore.getItemAsync(contextKey);
    if (!storedContext) return null;
    const context = JSON.parse(storedContext) as BackgroundContext;
    if (
      !context ||
      typeof context.routeId !== "string" ||
      !context.routeId ||
      typeof context.settings?.baseUrl !== "string" ||
      !context.settings.baseUrl ||
      typeof context.settings?.sessionToken !== "string" ||
      !context.settings.sessionToken
    ) {
      return null;
    }
    return context;
  } catch {
    return null;
  }
}
