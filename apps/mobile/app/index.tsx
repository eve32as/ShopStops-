import * as Location from "expo-location";
import * as SecureStore from "expo-secure-store";
import { useStripe } from "@stripe/stripe-react-native";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useIsFocused } from "expo-router";
import {
  AppState,
  Image,
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { ApiError, apiRequest, idempotencyKey, type ApiSettings } from "../src/api";
import {
  getDriverLocationContext,
  isDriverLocationUpdatesActive,
  startDriverLocationUpdates,
  stopDriverLocationUpdates,
} from "../src/backgroundLocation";

type Role = "Shopper" | "Driver" | "Merchant";
type NearbyMerchant = {
  merchantId: string;
  name: string;
  merchantType: string;
  storeLocationId: string;
  distanceMeters: number;
};
type CatalogItem = {
  id: string;
  name: string;
  description: string;
  unitPriceCents: number;
  volumeLiters: string;
};
type MerchantCatalogItem = CatalogItem & {
  storeLocationId: string;
  available: boolean;
};
type MerchantStoreLocation = {
  id: string;
  name: string;
};
type MerchantOrder = {
  id: string;
  storeLocationId: string;
  storeName: string;
  status: "placed" | "preparing" | "ready";
  deliveryWindowStart: string;
  deliveryWindowEnd: string;
  items: { name: string; quantity: number; unitPriceCents: number }[];
};
type MerchantCatalogDraft = {
  name: string;
  description: string;
  unitPriceCents: string;
  volumeLiters: string;
};
type CreateOrderBody = {
  storeLocationId: string;
  dropoff: { latitude: number; longitude: number };
  deliveryWindowStart: string;
  deliveryWindowEnd: string;
  items: { catalogItemId: string; quantity: number }[];
};
type DeliveryWindow = {
  start: string;
  end: string;
};
type Checkout = {
  id: string;
  status: string;
  currency: string;
  totalCents: number;
  invoiceLines: { type: string; merchantId?: string; amountCents: number }[];
};
type Route = {
  id: string;
  checkoutId: string;
  status: string;
  routingProvider: string;
  stops: {
    id: string;
    sequence: number;
    type: string;
    latitude: number;
    longitude: number;
    status: string;
  }[];
};
type StopEvent = {
  stopId: string;
  stopStatus: string;
  routeStatus: string;
};
type OrderLifecycleEvent = {
  id: number;
  type: string;
  aggregateType: string;
  aggregateId: string;
  createdAt: string;
  data: Record<string, unknown>;
};
type OrderProgress = {
  events: OrderLifecycleEvent[];
  status: string;
  nextAfterId: number;
  error: string;
};
type Subscription = {
  status: string;
  monthlyAmountCents: number;
  currency: string;
  storefrontVisible: boolean;
  currentPeriodEnd: string | null;
  paymentMethodReady: boolean;
};

const stripeKeyConfigured = Boolean(process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY);
const shopperOrderIDsKey = "shopstops.shopper-order-ids.v1";

function createDeliveryWindow(): DeliveryWindow {
  const start = new Date(Date.now() + 15 * 60 * 1000);
  return {
    start: start.toISOString(),
    end: new Date(start.getTime() + 2 * 60 * 60 * 1000).toISOString(),
  };
}

function isUUID(value: string) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
}

function merchantCatalogDraft(item: MerchantCatalogItem): MerchantCatalogDraft {
  return {
    name: item.name,
    description: item.description,
    unitPriceCents: String(item.unitPriceCents),
    volumeLiters: item.volumeLiters,
  };
}

function isValidCatalogVolume(value: number) {
  return Number.isFinite(value) && value > 0 && value <= 100000 &&
    Math.abs(value * 100 - Math.round(value * 100)) < 1e-8;
}

export default function HomeScreen() {
  const isFocused = useIsFocused();
  const [role, setRole] = useState<Role>("Shopper");
  const [appActive, setAppActive] = useState(AppState.currentState === "active");
  const [connectionOpen, setConnectionOpen] = useState(true);
  const [gatewayUrl, setGatewayUrl] = useState("");
  const [sessionToken, setSessionToken] = useState("");
  const settings = useMemo<ApiSettings>(
    () => ({ baseUrl: gatewayUrl, sessionToken }),
    [gatewayUrl, sessionToken],
  );
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [nearby, setNearby] = useState<NearbyMerchant[]>([]);
  const [nearbySearched, setNearbySearched] = useState(false);
  const [merchantTypeFilter, setMerchantTypeFilter] = useState("All");
  const [catalog, setCatalog] = useState<CatalogItem[]>([]);
  const [catalogStoreID, setCatalogStoreID] = useState("");
  const [quantities, setQuantities] = useState<Record<string, number>>({});
  const [checkout, setCheckout] = useState<Checkout | null>(null);
  const [route, setRoute] = useState<Route | null>(null);
  const [assignedRoutes, setAssignedRoutes] = useState<Route[]>([]);
  const [subscription, setSubscription] = useState<Subscription | null>(null);
  const [merchantStoreLocations, setMerchantStoreLocations] = useState<MerchantStoreLocation[]>([]);
  const [merchantOrders, setMerchantOrders] = useState<MerchantOrder[]>([]);
  const [merchantCatalog, setMerchantCatalog] = useState<MerchantCatalogItem[]>([]);
  const [merchantCatalogDrafts, setMerchantCatalogDrafts] = useState<Record<string, MerchantCatalogDraft>>({});
  const [merchantStoreLocation, setMerchantStoreLocation] = useState("");
  const [newItemName, setNewItemName] = useState("");
  const [newItemDescription, setNewItemDescription] = useState("");
  const [newItemPrice, setNewItemPrice] = useState("");
  const [newItemVolume, setNewItemVolume] = useState("");
  const [anchorStore, setAnchorStore] = useState("");
  const [orderIds, setOrderIds] = useState("");
  const [orderProgress, setOrderProgress] = useState<Record<string, OrderProgress>>({});
  const [latitude, setLatitude] = useState("");
  const [longitude, setLongitude] = useState("");
  const [routeId, setRouteId] = useState("");
  const [tracking, setTracking] = useState(false);
  const [deliveryWindow, setDeliveryWindow] = useState<DeliveryWindow>(() => createDeliveryWindow());
  const basketWindow = useRef<DeliveryWindow | null>(null);
  const orderAttempt = useRef<{ key: string; fingerprint: string; body: CreateOrderBody } | null>(null);
  const merchantCatalogAttempt = useRef<{
    key: string;
    fingerprint: string;
    body: {
      storeLocationId: string;
      name: string;
      description: string;
      unitPriceCents: number;
      volumeLiters: number;
    };
  } | null>(null);
  const routeStopAttempts = useRef(new Map<string, { eventId: string; observedAt: string }>());
  const checkoutAttempt = useRef<{
    key: string;
    body: {
      anchorStoreLocationId: string;
      orderIds: string[];
      dropoff: { latitude: number; longitude: number };
      deliveryWindowStart: string;
      deliveryWindowEnd: string;
    };
  } | null>(null);
  const previousConnection = useRef({ gatewayUrl, sessionToken });
  const orderIDsEdited = useRef(false);
  const terminalOrders = useRef(new Set<string>());
  const { initPaymentSheet, presentPaymentSheet } = useStripe();
  const trackedOrderIDs = useMemo(
    () => [...new Set(orderIds.split(/[\s,]+/).map((id) => id.trim()).filter(isUUID))],
    [orderIds],
  );

  const run = useCallback(async <T,>(action: () => Promise<T>) => {
    setBusy(true);
    setMessage("");
    try {
      return await action();
    } catch (error) {
      setMessage(error instanceof ApiError ? error.message : "The request could not be completed.");
      return undefined;
    } finally {
      setBusy(false);
    }
  }, []);

  function saveOrderIDs(value: string) {
    orderIDsEdited.current = true;
    setOrderIds(value);
    const ids = [...new Set(value.split(/[\s,]+/).map((id) => id.trim()).filter(isUUID))];
    if (ids.length === 0) {
      void SecureStore.deleteItemAsync(shopperOrderIDsKey).catch(() => undefined);
    } else {
      void SecureStore.setItemAsync(shopperOrderIDsKey, JSON.stringify(ids)).catch(() => undefined);
    }
  }

  useEffect(() => {
    let active = true;
    void SecureStore.getItemAsync(shopperOrderIDsKey).then((saved) => {
      if (!active || orderIDsEdited.current || !saved) return;
      try {
        const parsed: unknown = JSON.parse(saved);
        if (Array.isArray(parsed) && parsed.every((id) => typeof id === "string" && isUUID(id))) {
          setOrderIds([...new Set(parsed)].join(", "));
        }
      } catch {
        void SecureStore.deleteItemAsync(shopperOrderIDsKey).catch(() => undefined);
      }
    }).catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    const changed =
      previousConnection.current.gatewayUrl !== gatewayUrl ||
      previousConnection.current.sessionToken !== sessionToken;
    if (changed) {
      const hadConnection = Boolean(previousConnection.current.sessionToken);
      previousConnection.current = { gatewayUrl, sessionToken };
      if (!hadConnection) return;
      void endTracking();
      const previousOrderIDs = orderIds.split(/[\s,]+/).map((id) => id.trim()).filter(isUUID);
      void Promise.all(previousOrderIDs.map((id) =>
        SecureStore.deleteItemAsync(`shopstops.order-cursor.${id}`).catch(() => undefined),
      ));
      orderIDsEdited.current = true;
      void SecureStore.deleteItemAsync(shopperOrderIDsKey).catch(() => undefined);
      checkoutAttempt.current = null;
      orderAttempt.current = null;
      basketWindow.current = null;
      setDeliveryWindow(createDeliveryWindow());
      setNearby([]);
      setNearbySearched(false);
      setMerchantTypeFilter("All");
      setCatalog([]);
      setCatalogStoreID("");
      setQuantities({});
      setOrderIds("");
      setOrderProgress({});
      terminalOrders.current.clear();
      setCheckout(null);
      setRoute(null);
      setRouteId("");
      setAssignedRoutes([]);
      routeStopAttempts.current.clear();
      setSubscription(null);
      setMerchantCatalog([]);
      setMerchantOrders([]);
      setMerchantCatalogDrafts({});
      setMerchantStoreLocations([]);
      setMerchantStoreLocation("");
    }
  }, [gatewayUrl, sessionToken, orderIds]);

  async function findNearby() {
    const results = await run(() =>
      apiRequest<NearbyMerchant[]>(
        settings,
        `stores/${encodeURIComponent(anchorStore)}/nearby-merchants?radiusMeters=500`,
      ),
    );
    if (results) {
      setNearby(results);
      setNearbySearched(true);
      setMerchantTypeFilter("All");
    }
  }

  async function loadCatalog(storeID = catalogStoreID) {
    const normalizedID = storeID.trim();
    if (!normalizedID) {
      setMessage("Enter a store location ID to browse its catalog.");
      return;
    }
    const results = await run(() =>
      apiRequest<CatalogItem[]>(
        settings,
        `stores/${encodeURIComponent(normalizedID)}/catalog`,
      ),
    );
    if (results) {
      setCatalogStoreID(normalizedID);
      setCatalog(results);
      setQuantities({});
    }
  }

  async function createCustomerOrder() {
    if (checkout) {
      setMessage("Start a new basket before creating another order.");
      return;
    }
    const lat = Number(latitude);
    const lon = Number(longitude);
    const items = catalog
      .filter((item) => (quantities[item.id] ?? 0) > 0)
      .map((item) => ({ catalogItemId: item.id, quantity: quantities[item.id] }));
    if (
      !catalogStoreID.trim() || !Number.isFinite(lat) || lat < -90 || lat > 90 ||
      !Number.isFinite(lon) || lon < -180 || lon > 180 || items.length === 0
    ) {
      setMessage("Choose catalog items and enter valid delivery coordinates.");
      return;
    }
    const window = basketWindow.current ?? createDeliveryWindow();
    basketWindow.current = window;
    setDeliveryWindow(window);
    const body: CreateOrderBody = {
      storeLocationId: catalogStoreID.trim(),
      dropoff: { latitude: lat, longitude: lon },
      deliveryWindowStart: window.start,
      deliveryWindowEnd: window.end,
      items,
    };
    const fingerprint = JSON.stringify(body);
    if (orderAttempt.current?.fingerprint !== fingerprint) {
      orderAttempt.current = { key: idempotencyKey(), fingerprint, body };
    }
    const attempt = orderAttempt.current;
    const result = await run(() =>
      apiRequest<{ id: string; status: string; itemSubtotalCents: number }>(
        settings,
        "orders",
        {
          method: "POST",
          headers: { "Idempotency-Key": attempt.key },
          body: JSON.stringify(attempt.body),
        },
      ),
    );
    if (result) {
      orderAttempt.current = null;
      checkoutAttempt.current = null;
      const currentIDs = orderIds.split(/[\s,]+/).map((id) => id.trim()).filter(Boolean);
      if (!currentIDs.includes(result.id)) {
        saveOrderIDs([...currentIDs, result.id].join(", "));
      }
      setQuantities({});
      setMessage(`Order created · $${(result.itemSubtotalCents / 100).toFixed(2)} items.`);
    }
  }

  async function createCheckout() {
    const ids = orderIds
      .split(/[\s,]+/)
      .map((id) => id.trim())
      .filter(Boolean);
    const lat = Number(latitude);
    const lon = Number(longitude);
    if (!anchorStore.trim() || ids.length === 0 || !Number.isFinite(lat) || !Number.isFinite(lon)) {
      setMessage("Enter the anchor store, order IDs, and delivery coordinates.");
      return;
    }
    if (!checkoutAttempt.current) {
      const window = basketWindow.current ?? createDeliveryWindow();
      basketWindow.current = window;
      setDeliveryWindow(window);
      checkoutAttempt.current = {
        key: idempotencyKey(),
        body: {
          anchorStoreLocationId: anchorStore.trim(),
          orderIds: ids,
          dropoff: { latitude: lat, longitude: lon },
          deliveryWindowStart: window.start,
          deliveryWindowEnd: window.end,
        },
      };
    }
    const attempt = checkoutAttempt.current;
    const result = await run(() =>
      apiRequest<Checkout>(settings, "checkouts", {
        method: "POST",
        headers: { "Idempotency-Key": attempt.key },
        body: JSON.stringify(attempt.body),
      }),
    );
    if (result) {
      checkoutAttempt.current = null;
      setCheckout(result);
    }
  }

  function startNewBasket() {
    const oldOrderIDs = orderIds.split(/[\s,]+/).map((id) => id.trim()).filter(isUUID);
    void Promise.all(oldOrderIDs.map((id) =>
      SecureStore.deleteItemAsync(`shopstops.order-cursor.${id}`).catch(() => undefined),
    ));
    orderIDsEdited.current = true;
    void SecureStore.deleteItemAsync(shopperOrderIDsKey).catch(() => undefined);
    orderAttempt.current = null;
    checkoutAttempt.current = null;
    basketWindow.current = null;
    setDeliveryWindow(createDeliveryWindow());
    setOrderIds("");
    setOrderProgress({});
    terminalOrders.current.clear();
    setCheckout(null);
    setQuantities({});
    setMessage("Started a new basket.");
  }

  async function payCheckout() {
    if (!checkout || !stripeKeyConfigured) return;
    const intent = await run(() =>
      apiRequest<{ clientSecret: string; amountCents: number; currency: string }>(
        settings,
        `checkouts/${encodeURIComponent(checkout.id)}/payment`,
        { method: "POST" },
      ),
    );
    if (!intent) return;
    const initialized = await initPaymentSheet({
      merchantDisplayName: "ShopStops",
      paymentIntentClientSecret: intent.clientSecret,
    });
    if (initialized.error) {
      setMessage(initialized.error.message);
      return;
    }
    const presented = await presentPaymentSheet();
    if (presented.error) setMessage(presented.error.message);
    else setMessage("Payment submitted. Your order updates when Stripe confirms it.");
  }

  async function loadRoute(routeID = routeId.trim()) {
    if (!isUUID(routeID)) {
      setMessage("Enter your assigned route ID.");
      return;
    }
    const result = await run(() =>
      apiRequest<Route>(settings, `driver/routes/${encodeURIComponent(routeID)}`),
    );
    if (result) {
      setRoute(result);
      setRouteId(result.id);
    }
  }

  async function loadAssignedRoutes() {
    const result = await run(() => apiRequest<Route[]>(settings, "driver/routes"));
    if (result) setAssignedRoutes(result);
  }

  async function updateRouteStop(stopId: string, type: "arrive" | "complete") {
    const routeKey = routeId.trim();
    const attemptKey = `${routeKey}:${stopId}:${type}`;
    const attempt = routeStopAttempts.current.get(attemptKey) ?? {
      eventId: idempotencyKey(),
      observedAt: new Date().toISOString(),
    };
    routeStopAttempts.current.set(attemptKey, attempt);
    const result = await run(() =>
      apiRequest<StopEvent>(
        settings,
        `driver/routes/${encodeURIComponent(routeKey)}/stops/${encodeURIComponent(stopId)}/events`,
        {
          method: "POST",
          body: JSON.stringify({
            eventId: attempt.eventId,
            type,
            observedAt: attempt.observedAt,
          }),
        },
      ),
    );
    if (result) {
      routeStopAttempts.current.delete(attemptKey);
      setRoute((current) =>
        current
          ? {
              ...current,
              status: result.routeStatus,
              stops: current.stops.map((stop) =>
                stop.id === result.stopId ? { ...stop, status: result.stopStatus } : stop,
              ),
            }
          : current,
      );
      if (result.routeStatus === "completed" && tracking) {
        await endTracking();
      }
    }
  }

  async function beginTracking() {
    const activeRouteID = routeId.trim();
    if (!activeRouteID || route?.id !== activeRouteID || route.status !== "in_progress") {
      setMessage("Load your assigned route before starting location sharing.");
      return;
    }
    const permission = await Location.requestForegroundPermissionsAsync();
    if (permission.status !== "granted") {
      setMessage("Allow location access while using the app to share your active route.");
      return;
    }
    try {
      const backgroundPermission = await Location.requestBackgroundPermissionsAsync();
      if (backgroundPermission.status !== "granted") {
        setMessage("Allow background location access in device settings to share your route while the app is backgrounded.");
        return;
      }
      await startDriverLocationUpdates(activeRouteID, settings);
      setTracking(true);
      setMessage("Background location sharing is active for your assigned route.");
    } catch {
      setMessage("Could not start background location sharing. Check device settings and try again.");
    }
  }

  async function endTracking() {
    try {
      await stopDriverLocationUpdates();
    } catch {
      setMessage("Could not stop location sharing. Try again from this screen.");
      return;
    }
    setTracking(false);
    setMessage("Location sharing stopped and the saved gateway session was cleared.");
  }

  useEffect(() => {
    if (role !== "Driver" && tracking) {
      void endTracking();
    }
  }, [role, tracking]);

  useEffect(() => {
    const subscription = AppState.addEventListener("change", (state) => {
      setAppActive(state === "active");
    });
    return () => subscription.remove();
  }, []);

  useEffect(() => {
    if (
      role !== "Shopper" || !isFocused || !appActive ||
      !gatewayUrl.trim() || !sessionToken.trim() || trackedOrderIDs.length === 0
    ) {
      return;
    }
    let stopped = false;
    const controllers = new Map<string, AbortController>();
    const cursorKey = (orderID: string) => `shopstops.order-cursor.${orderID}`;

    async function pollOrder(orderID: string) {
      let afterID = 0;
      let status = "Waiting for updates";
      try {
        const savedCursor = await SecureStore.getItemAsync(cursorKey(orderID));
        if (savedCursor) {
          try {
            const saved: unknown = JSON.parse(savedCursor);
            if (saved && typeof saved === "object") {
              const cursor = saved as { afterId?: unknown; terminal?: unknown; status?: unknown };
              if (typeof cursor.afterId === "number" && Number.isSafeInteger(cursor.afterId) && cursor.afterId >= 0) {
                afterID = cursor.afterId;
              }
              if (typeof cursor.terminal === "boolean" && cursor.terminal) {
                terminalOrders.current.add(orderID);
              }
              if (typeof cursor.status === "string") status = cursor.status;
            } else if (/^\d+$/.test(savedCursor)) {
              afterID = Number(savedCursor);
            }
          } catch {
            if (/^\d+$/.test(savedCursor)) afterID = Number(savedCursor);
          }
        }
        setOrderProgress((current) => ({
          ...current,
          [orderID]: {
            events: current[orderID]?.events ?? [],
            status,
            nextAfterId: afterID,
            error: "",
          },
        }));
      } catch {
        setOrderProgress((current) => ({
          ...current,
          [orderID]: {
            events: current[orderID]?.events ?? [],
            status: current[orderID]?.status ?? "Updates unavailable",
            nextAfterId: current[orderID]?.nextAfterId ?? 0,
            error: "Could not restore the saved update cursor.",
          },
        }));
      }

      while (!stopped && !terminalOrders.current.has(orderID)) {
        const controller = new AbortController();
        controllers.set(orderID, controller);
        try {
          const result = await apiRequest<{
            events: OrderLifecycleEvent[];
            nextAfterId: number;
          }>(settings, `orders/${encodeURIComponent(orderID)}/events?waitSeconds=20`, {
            method: "GET",
            headers: { "Last-Event-ID": String(afterID) },
            signal: controller.signal,
          });
          if (stopped) return;
          afterID = result.nextAfterId;
          if (result.events.length > 0) {
            let terminal = false;
            for (const event of result.events) {
              const orderState = event.data.state;
              if (typeof orderState === "string") {
                status = orderState.replaceAll("_", " ");
                if (orderState === "delivered" || orderState === "cancelled") terminal = true;
              }
              const routeStatus = event.data.routeStatus;
              if (typeof routeStatus === "string") {
                status = routeStatus.replaceAll("_", " ");
                if (routeStatus === "completed" || routeStatus === "cancelled") terminal = true;
              }
              if (event.type === "delivery.bundle.activated") status = "dispatched";
              if (event.type === "delivery.route.stop.arrived") {
                status = event.data.stopType === "dropoff" ? "at your delivery" : "at a pickup";
              }
              if (event.type === "delivery.route.stop.completed" && !terminal) {
                status = event.data.stopType === "dropoff" ? "delivery stop complete" : "pickup complete";
              }
            }
            if (status === "Waiting for updates") status = "Updates received";
            setOrderProgress((current) => {
              const previous = current[orderID];
              const knownIDs = new Set((previous?.events ?? []).map((event) => event.id));
              const combined = [
                ...(previous?.events ?? []),
                ...result.events.filter((event) => !knownIDs.has(event.id)),
              ].slice(-20);
              return {
                ...current,
                [orderID]: {
                  events: combined,
                  status: status || previous?.status || "Updates received",
                  nextAfterId: afterID,
                  error: "",
                },
              };
            });
            if (terminal) terminalOrders.current.add(orderID);
          }
          try {
            await SecureStore.setItemAsync(cursorKey(orderID), JSON.stringify({
              afterId: afterID,
              terminal: terminalOrders.current.has(orderID),
              status,
            }));
          } catch {
            setOrderProgress((current) => ({
              ...current,
              [orderID]: {
                events: current[orderID]?.events ?? [],
                status: current[orderID]?.status ?? status,
                nextAfterId: afterID,
                error: "Updates are live, but the cursor could not be saved on this device.",
              },
            }));
          }
        } catch (error) {
          if (stopped || controller.signal.aborted) return;
          const message = error instanceof ApiError ? error.message : "Connection interrupted; retrying.";
          setOrderProgress((current) => ({
            ...current,
            [orderID]: {
              events: current[orderID]?.events ?? [],
              status: current[orderID]?.status ?? "Waiting for updates",
              nextAfterId: afterID,
              error: message,
            },
          }));
          if (error instanceof ApiError && [401, 403, 404].includes(error.status ?? 0)) return;
          await new Promise((resolve) => setTimeout(resolve, 2000));
        } finally {
          controllers.delete(orderID);
        }
      }
    }

    void Promise.all(trackedOrderIDs.map((orderID) => pollOrder(orderID)));
    return () => {
      stopped = true;
      for (const controller of controllers.values()) controller.abort();
      controllers.clear();
    };
  }, [
    role, isFocused, appActive, gatewayUrl, sessionToken, trackedOrderIDs, settings,
  ]);

  useEffect(() => {
    let mounted = true;
    void getDriverLocationContext().then((context) => {
      if (!mounted) return;
      if (!context) {
        void isDriverLocationUpdatesActive().then((active) => {
          if (!mounted) return;
          setTracking(active);
          if (active) setRole("Driver");
        });
        return;
      }
      setGatewayUrl(context.settings.baseUrl);
      setSessionToken(context.settings.sessionToken);
      setRouteId(context.routeId);
      setRole("Driver");
      setTracking(true);
      void apiRequest<Route>(
        context.settings,
        `driver/routes/${encodeURIComponent(context.routeId)}`,
      ).then((activeRoute) => {
        if (mounted) setRoute(activeRoute);
      }).catch((error) => {
        if (mounted) setMessage(error instanceof ApiError ? error.message : "Could not restore the active route.");
      });
    });
    const syncTracking = () => {
      void isDriverLocationUpdatesActive().then((active) => {
        if (!mounted) return;
        setTracking(active);
        if (!active) {
          setRoute(null);
          setRouteId("");
        }
      });
    };
    const subscription = AppState.addEventListener("change", (state) => {
      if (state === "active") syncTracking();
    });
    return () => {
      mounted = false;
      subscription.remove();
    };
  }, []);

  async function refreshSubscription() {
    const result = await run(() => apiRequest<Subscription>(settings, "merchant/subscription"));
    if (result) setSubscription(result);
  }

  async function loadMerchantOrders() {
    const orders = await run(() =>
      apiRequest<MerchantOrder[]>(settings, "merchant/orders"),
    );
    if (orders) setMerchantOrders(orders);
  }

  async function updateMerchantOrderState(order: MerchantOrder, state: "preparing" | "ready") {
    const updated = await run(() =>
      apiRequest<{ id: string; status: MerchantOrder["status"] }>(
        settings,
        `merchant/orders/${encodeURIComponent(order.id)}`,
        { method: "PATCH", body: JSON.stringify({ state }) },
      ),
    );
    if (updated) {
      setMerchantOrders((current) =>
        current.map((item) => item.id === updated.id ? { ...item, status: updated.status } : item),
      );
    }
  }

  async function setupMerchantPaymentMethod() {
    const intent = await run(() =>
      apiRequest<{ clientSecret: string }>(settings, "merchant/subscription/setup-intent", {
        method: "POST",
        headers: { "Idempotency-Key": idempotencyKey() },
      }),
    );
    if (!intent) return;
    const initialized = await initPaymentSheet({
      merchantDisplayName: "ShopStops Merchant",
      setupIntentClientSecret: intent.clientSecret,
    });
    if (initialized.error) {
      setMessage(initialized.error.message);
      return;
    }
    const presented = await presentPaymentSheet();
    if (presented.error) {
      setMessage(presented.error.message);
      return;
    }
    setMessage("Billing method saved. Refresh status before starting your subscription.");
    await refreshSubscription();
  }

  async function startSubscription() {
    const result = await run(() =>
      apiRequest<{ status: string }>(settings, "merchant/subscription", { method: "POST" }),
    );
    if (result) {
      setMessage(`Subscription is ${result.status}. Storefront activation waits for a paid invoice.`);
      await refreshSubscription();
    }
  }

  async function loadMerchantCatalog() {
    const items = await run(() =>
      apiRequest<MerchantCatalogItem[]>(settings, "merchant/catalog"),
    );
    if (items) {
      setMerchantCatalog(items);
      setMerchantCatalogDrafts(Object.fromEntries(items.map((item) => [
        item.id, merchantCatalogDraft(item),
      ])));
    }
  }

  async function loadMerchantStoreLocations() {
    const locations = await run(() =>
      apiRequest<MerchantStoreLocation[]>(settings, "merchant/store-locations"),
    );
    if (locations) {
      setMerchantStoreLocations(locations);
      if (!merchantStoreLocation && locations.length > 0) {
        setMerchantStoreLocation(locations[0].id);
      }
    }
  }

  async function createMerchantCatalogItem() {
    const price = Number(newItemPrice);
    const volume = Number(newItemVolume);
    if (!merchantStoreLocation.trim() || !newItemName.trim() || newItemName.trim().length > 200 ||
        newItemDescription.length > 1000 || !Number.isInteger(price) || price < 1 ||
        price > 10000000 || !isValidCatalogVolume(volume)) {
      setMessage("Enter a store, item name (up to 200 characters), description (up to 1,000 characters), price (1–10,000,000 cents), and volume (over 0, up to 100,000 liters in 0.01-liter increments).");
      return;
    }
    const body = {
      storeLocationId: merchantStoreLocation.trim(),
      name: newItemName.trim(),
      description: newItemDescription,
      unitPriceCents: price,
      volumeLiters: volume,
    };
    const fingerprint = JSON.stringify(body);
    if (merchantCatalogAttempt.current?.fingerprint !== fingerprint) {
      merchantCatalogAttempt.current = { key: idempotencyKey(), fingerprint, body };
    }
    const attempt = merchantCatalogAttempt.current;
    const item = await run(() =>
      apiRequest<MerchantCatalogItem>(settings, "merchant/catalog", {
        method: "POST",
        headers: { "Idempotency-Key": attempt.key },
        body: JSON.stringify(attempt.body),
      }),
    );
    if (item) {
      merchantCatalogAttempt.current = null;
      setNewItemName("");
      setNewItemDescription("");
      setNewItemPrice("");
      setNewItemVolume("");
      setMessage("Catalog item saved.");
      await loadMerchantCatalog();
    }
  }

  async function updateMerchantCatalogItem(item: MerchantCatalogItem) {
    const draft = merchantCatalogDrafts[item.id];
    if (!draft) return;
    const price = Number(draft.unitPriceCents);
    const volume = Number(draft.volumeLiters);
    if (!draft.name.trim() || draft.name.trim().length > 200 || draft.description.length > 1000 ||
        !Number.isInteger(price) || price < 1 || price > 10000000 || !isValidCatalogVolume(volume)) {
      setMessage("Enter an item name (up to 200 characters), description (up to 1,000 characters), price (1–10,000,000 cents), and volume (over 0, up to 100,000 liters in 0.01-liter increments).");
      return;
    }
    const updated = await run(() =>
      apiRequest<MerchantCatalogItem>(
        settings,
        `merchant/catalog/${encodeURIComponent(item.id)}`,
        {
          method: "PATCH",
          body: JSON.stringify({
            ...draft,
            name: draft.name.trim(),
            unitPriceCents: price,
            volumeLiters: volume,
            available: item.available,
          }),
        },
      ),
    );
    if (updated) {
      setMerchantCatalog((current) => current.map((entry) =>
        entry.id === updated.id ? updated : entry,
      ));
      setMerchantCatalogDrafts((current) => ({
        ...current,
        [updated.id]: merchantCatalogDraft(updated),
      }));
      setMessage("Catalog item updated.");
    }
  }

  async function toggleMerchantCatalogAvailability(item: MerchantCatalogItem) {
    const draft = merchantCatalogDrafts[item.id];
    if (!draft) return;
    const price = Number(draft.unitPriceCents);
    const volume = Number(draft.volumeLiters);
    if (!draft.name.trim() || draft.name.trim().length > 200 || draft.description.length > 1000 ||
        !Number.isInteger(price) || price < 1 || price > 10000000 || !isValidCatalogVolume(volume)) {
      setMessage("Save valid item details before changing customer availability.");
      return;
    }
    const updated = await run(() =>
      apiRequest<MerchantCatalogItem>(
        settings,
        `merchant/catalog/${encodeURIComponent(item.id)}`,
        {
          method: "PATCH",
          body: JSON.stringify({
            ...draft,
            unitPriceCents: price,
            volumeLiters: volume,
            available: !item.available,
          }),
        },
      ),
    );
    if (updated) {
      setMerchantCatalog((current) => current.map((entry) =>
        entry.id === updated.id ? updated : entry,
      ));
      setMerchantCatalogDrafts((current) => ({
        ...current,
        [updated.id]: merchantCatalogDraft(updated),
      }));
    }
  }

  const sharedForm = (
    <View style={styles.configCard}>
      <Text style={styles.sectionEyebrow}>SECURE CONNECTION</Text>
      <Text style={styles.sectionTitle}>Connect to your ShopStops gateway</Text>
      <Text style={styles.helpText}>
        Use the public HTTPS gateway and a user session from your identity provider. Never enter
        the backend service token here. While background location is active, the session is held in
        OS secure storage and cleared when sharing stops.
      </Text>
      <TextInput
        accessibilityLabel="Gateway URL"
        autoCapitalize="none"
        autoCorrect={false}
        keyboardType="url"
        onChangeText={setGatewayUrl}
        placeholder="https://api.example.com/v1"
        placeholderTextColor="#94a3b8"
        style={styles.input}
        value={gatewayUrl}
      />
      <TextInput
        accessibilityLabel="Session token"
        autoCapitalize="none"
        autoCorrect={false}
        onChangeText={setSessionToken}
        placeholder="User session token"
        placeholderTextColor="#94a3b8"
        secureTextEntry
        style={styles.input}
        value={sessionToken}
      />
    </View>
  );
  const activeStop = route?.stops
    .slice()
    .sort((a, b) => a.sequence - b.sequence)
    .find((stop) => stop.status !== "completed");
  const merchantTypes = [...new Set(nearby.map((merchant) => merchant.merchantType).filter(Boolean))]
    .sort((left, right) => left.localeCompare(right));
  const filteredNearby = nearby.filter(
    (merchant) => merchantTypeFilter === "All" || merchant.merchantType === merchantTypeFilter,
  );

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={styles.screen}
    >
      <ScrollView contentContainerStyle={styles.content} keyboardShouldPersistTaps="handled">
        <View style={styles.header}>
          <View style={styles.brandLockup}>
            <Image accessible={false} source={require("../assets/icon.png")} style={styles.brandIcon} />
            <View>
              <Text style={styles.brand}>ShopStops</Text>
              <Text style={styles.tagline}>Neighborhood delivery, connected.</Text>
            </View>
          </View>
          <View style={styles.livePill}>
            <View style={styles.liveDot} />
            <Text style={styles.liveText}>BETA</Text>
          </View>
        </View>

        <View style={styles.hero}>
          <Text style={styles.heroEyebrow}>ONE CHECKOUT. MORE LOCAL.</Text>
          <Text style={styles.heroTitle}>Your neighborhood, on the way.</Text>
          <Text style={styles.heroCopy}>
            One local delivery for the restaurant and the shop next door.
          </Text>
          <View style={styles.heroStats}>
            <View><Text style={styles.statValue}>500m</Text><Text style={styles.statLabel}>local picks</Text></View>
            <View><Text style={styles.statValue}>2</Text><Text style={styles.statLabel}>stores per basket</Text></View>
            <View><Text style={styles.statValue}>1</Text><Text style={styles.statLabel}>shared route</Text></View>
          </View>
        </View>

        <View style={styles.rolePicker}>
          {(["Shopper", "Driver", "Merchant"] as const).map((item) => (
            <Pressable
              accessibilityRole="tab"
              accessibilityState={{ selected: role === item }}
              key={item}
              onPress={() => {
                setRole(item);
                setMessage("");
              }}
              style={[styles.roleButton, role === item && styles.roleButtonActive]}
            >
              <Text style={[styles.roleText, role === item && styles.roleTextActive]}>{item}</Text>
            </Pressable>
          ))}
        </View>

        <View style={styles.connectionCard}>
          <View style={styles.connectionHeader}>
            <View style={styles.connectionCopy}>
              <Text style={styles.sectionEyebrow}>SECURE CONNECTION</Text>
              <Text style={styles.connectionStatus}>
                {gatewayUrl && sessionToken ? "Gateway details entered" : "Connect to your gateway"}
              </Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityState={{ expanded: connectionOpen }}
              onPress={() => setConnectionOpen((open) => !open)}
              style={styles.connectionToggle}
            >
              <Text style={styles.connectionToggleText}>{connectionOpen ? "Hide" : "Edit"}</Text>
            </Pressable>
          </View>
          {connectionOpen ? sharedForm : null}
        </View>

        {message ? (
          <View style={styles.messageBox} accessibilityLiveRegion="polite">
            <Text style={styles.messageText}>{message}</Text>
          </View>
        ) : null}
        {busy ? <ActivityIndicator color="#e5653d" style={styles.loader} /> : null}

        {role === "Shopper" ? (
          <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>01 / DISCOVER</Text>
              <Text style={styles.sectionTitle}>Good things, close by.</Text>
              <Text style={styles.helpText}>
                Find subscription-active grocery and retail partners within 500 meters of your
                restaurant.
              </Text>
              <Field
                editable={!checkout && !orderIds.trim()}
                label="Anchor restaurant store ID"
                onChangeText={(value) => {
                  checkoutAttempt.current = null;
                  setAnchorStore(value);
                  setCatalogStoreID(value);
                  setNearby([]);
                  setNearbySearched(false);
                  setMerchantTypeFilter("All");
                }}
                value={anchorStore}
              />
              <View style={styles.actionRow}>
                <ActionButton disabled={busy} label="Find nearby shops" onPress={() => void findNearby()} />
                <ActionButton
                  disabled={busy}
                  label="Browse anchor catalog"
                  onPress={() => void loadCatalog(anchorStore)}
                  secondary
                />
              </View>
              {merchantTypes.length > 0 ? (
                <View style={styles.filterRow} accessibilityRole="tablist" accessibilityLabel="Filter nearby merchants by type">
                  {["All", ...merchantTypes].map((merchantType) => (
                    <Pressable
                      accessibilityRole="tab"
                      accessibilityState={{ selected: merchantTypeFilter === merchantType }}
                      key={merchantType}
                      onPress={() => setMerchantTypeFilter(merchantType)}
                      style={[
                        styles.filterChip,
                        merchantTypeFilter === merchantType && styles.filterChipActive,
                      ]}
                    >
                      <Text style={[
                        styles.filterChipText,
                        merchantTypeFilter === merchantType && styles.filterChipTextActive,
                      ]}>
                        {merchantType}
                      </Text>
                    </Pressable>
                  ))}
                </View>
              ) : null}
              {nearbySearched && nearby.length === 0 ? (
                <Text style={styles.tinyNote}>No nearby shops are available for this restaurant.</Text>
              ) : null}
              {nearbySearched && nearby.length > 0 && filteredNearby.length === 0 ? (
                <Text style={styles.tinyNote}>No nearby shops match this type.</Text>
              ) : null}
              {filteredNearby.map((merchant) => (
                <View key={merchant.merchantId}>
                  <View style={styles.resultRow}>
                    <View style={styles.resultIcon}><Text style={styles.resultIconText}>+</Text></View>
                    <View style={styles.resultCopy}>
                      <Text style={styles.resultTitle}>{merchant.name}</Text>
                      <Text style={styles.resultSub}>{merchant.merchantType} · {Math.round(merchant.distanceMeters)} m away</Text>
                    </View>
                  </View>
                  <ActionButton
                    disabled={busy}
                    label={`Browse ${merchant.name}`}
                    onPress={() => void loadCatalog(merchant.storeLocationId)}
                    secondary
                  />
                </View>
              ))}
            </View>

            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>02 / BUILD YOUR ORDER</Text>
              <Text style={styles.sectionTitle}>Choose items from the block.</Text>
              <Field
                editable={!checkout}
                label="Store location ID"
                onChangeText={(value) => {
                  setCatalogStoreID(value);
                  setCatalog([]);
                  setQuantities({});
                }}
                value={catalogStoreID}
              />
              <ActionButton disabled={busy || Boolean(checkout)} label="Load store catalog" onPress={() => void loadCatalog()} />
              {catalog.map((item) => {
                const quantity = quantities[item.id] ?? 0;
                return (
                  <View key={item.id} style={styles.catalogRow}>
                    <View style={styles.resultCopy}>
                      <Text style={styles.resultTitle}>{item.name}</Text>
                      {item.description ? <Text style={styles.resultSub}>{item.description}</Text> : null}
                      <Text style={styles.resultSub}>
                        ${(item.unitPriceCents / 100).toFixed(2)}
                      </Text>
                    </View>
                    <View style={styles.quantityControl}>
                      <Pressable
                        accessibilityLabel={`Decrease ${item.name} quantity`}
                        accessibilityRole="button"
                        disabled={quantity === 0}
                        onPress={() => setQuantities((current) => ({
                          ...current,
                          [item.id]: Math.max(0, (current[item.id] ?? 0) - 1),
                        }))}
                        style={styles.quantityButton}
                      ><Text style={styles.quantityButtonText}>−</Text></Pressable>
                      <Text style={styles.quantityText}>{quantity}</Text>
                      <Pressable
                        accessibilityLabel={`Increase ${item.name} quantity`}
                        accessibilityRole="button"
                        disabled={quantity >= 99}
                        onPress={() => setQuantities((current) => ({
                          ...current,
                          [item.id]: (current[item.id] ?? 0) + 1,
                        }))}
                        style={styles.quantityButton}
                      ><Text style={styles.quantityButtonText}>+</Text></Pressable>
                    </View>
                  </View>
                );
              })}
              <View style={styles.fieldGrid}>
                <Field editable={!checkout && !orderIds.trim()} label="Drop-off latitude" keyboardType="decimal-pad" onChangeText={setLatitude} value={latitude} />
                <Field editable={!checkout && !orderIds.trim()} label="Drop-off longitude" keyboardType="decimal-pad" onChangeText={setLongitude} value={longitude} />
              </View>
              <ActionButton
                disabled={busy || Boolean(checkout) || catalog.length === 0}
                label="Create order from selected items"
                onPress={() => void createCustomerOrder()}
              />
            </View>

            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>03 / CHECKOUT</Text>
              <Text style={styles.sectionTitle}>Bring the whole block together.</Text>
              <Text style={styles.helpText}>
                Create one order from each store catalog you want to include, then create one unified checkout.
              </Text>
              <Text style={styles.tinyNote}>
                Delivery window · {new Date(deliveryWindow.start).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
                {" – "}
                {new Date(deliveryWindow.end).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
              </Text>
              <Field
                editable={!checkout}
                label="Order IDs (comma separated)"
                onChangeText={(value) => {
                  checkoutAttempt.current = null;
                  saveOrderIDs(value);
                }}
                value={orderIds}
              />
              {trackedOrderIDs.map((orderID) => {
                const progress = orderProgress[orderID];
                return (
                  <View key={orderID} style={styles.orderProgressCard} accessibilityLiveRegion="polite">
                    <View style={styles.orderProgressHeader}>
                      <Text style={styles.orderProgressStatus}>
                        {progress?.status ?? "Waiting for order updates"}
                      </Text>
                      <Text style={styles.orderProgressLive}>
                        {terminalOrders.current.has(orderID) ? "COMPLETE" : "LIVE"}
                      </Text>
                    </View>
                    <Text style={styles.tinyNote}>Order · {orderID}</Text>
                    {progress?.error ? <Text style={styles.orderProgressError}>{progress.error}</Text> : null}
                    {progress?.events.slice(-3).reverse().map((event) => (
                      <Text key={event.id} style={styles.orderProgressEvent}>
                        {event.type.replaceAll(".", " ").replaceAll("_", " ")} ·{" "}
                        {new Date(event.createdAt).toLocaleTimeString([], {
                          hour: "numeric", minute: "2-digit",
                        })}
                      </Text>
                    ))}
                  </View>
                );
              })}
              <ActionButton
                disabled={busy || Boolean(checkout)}
                label={checkout ? "Basket created" : "Create basket"}
                onPress={() => void createCheckout()}
              />
              {checkout ? (
                <View style={styles.checkoutBox}>
                  <Text style={styles.checkoutLabel}>BASKET {checkout.id}</Text>
                  {checkout.invoiceLines.map((line, index) => (
                    <View key={`${line.type}-${index}`} style={styles.invoiceRow}>
                      <Text style={styles.invoiceLabel}>{line.type.replaceAll("_", " ")}</Text>
                      <Text style={styles.invoiceAmount}>
                        {(line.amountCents / 100).toLocaleString("en-US", {
                          style: "currency",
                          currency: checkout.currency.toUpperCase(),
                        })}
                      </Text>
                    </View>
                  ))}
                  <View style={[styles.invoiceRow, styles.totalRow]}>
                    <Text style={styles.totalLabel}>Total</Text>
                    <Text style={styles.totalValue}>
                      {(checkout.totalCents / 100).toLocaleString("en-US", {
                        style: "currency",
                        currency: checkout.currency.toUpperCase(),
                      })}
                    </Text>
                  </View>
                  <ActionButton
                    disabled={busy || !stripeKeyConfigured}
                    label={stripeKeyConfigured ? "Continue to secure payment" : "Add Stripe publishable key to pay"}
                    onPress={() => void payCheckout()}
                  />
                  <Text style={styles.tinyNote}>Checkout ID · {checkout.id}</Text>
                </View>
              ) : null}
              {basketWindow.current || orderIds.trim() || checkout ? (
                <ActionButton
                  disabled={busy}
                  label="Start a new basket"
                  onPress={startNewBasket}
                  secondary
                />
              ) : null}
            </View>
          </>
        ) : null}

        {role === "Driver" ? (
          <View style={styles.card}>
            <Text style={styles.sectionEyebrow}>DRIVER / ACTIVE DELIVERY</Text>
            <Text style={styles.sectionTitle}>Every stop, in the right order.</Text>
            <Text style={styles.helpText}>
              Refresh to see routes assigned to your signed-in driver identity. Share location about
              every five seconds while a route is active; OS scheduling may delay updates. Your
              gateway session is stored securely only while sharing is active.
            </Text>
            <ActionButton
              disabled={busy || tracking}
              label="Refresh my assigned routes"
              onPress={() => void loadAssignedRoutes()}
              secondary
            />
            {assignedRoutes.length === 0 ? (
              <Text style={styles.tinyNote}>No active routes are assigned to this driver.</Text>
            ) : (
              assignedRoutes.map((assignedRoute) => (
                <View key={assignedRoute.id} style={styles.merchantItem}>
                  <Text style={styles.resultTitle}>Route · {assignedRoute.status.replaceAll("_", " ")}</Text>
                  <Text style={styles.resultSub}>Checkout · {assignedRoute.checkoutId}</Text>
                  <Text style={styles.resultSub}>{assignedRoute.stops.length} ordered stops</Text>
                  <ActionButton
                    disabled={busy || tracking}
                    label={route?.id === assignedRoute.id ? "Route loaded" : "Open assigned route"}
                    onPress={() => void loadRoute(assignedRoute.id)}
                    secondary
                  />
                </View>
              ))
            )}
            <Field
              editable={!tracking && !busy}
              label="Assigned route ID"
              onChangeText={(value) => {
                setRouteId(value);
                setRoute(null);
                routeStopAttempts.current.clear();
              }}
              value={routeId}
            />
            <ActionButton disabled={busy || tracking} label="Load route" onPress={() => void loadRoute()} />
            {route ? (
              <View style={styles.routeBox}>
                <Text style={styles.checkoutLabel}>ROUTE · {route.status.toUpperCase()}</Text>
                {route.stops
                  .slice()
                  .sort((a, b) => a.sequence - b.sequence)
                  .map((stop, index) => (
                    <View key={stop.id} style={styles.stopRow}>
                      <View style={[styles.stopNumber, index === route.stops.length - 1 && styles.stopNumberLast]}>
                        <Text style={styles.stopNumberText}>{stop.sequence}</Text>
                      </View>
                      <View style={styles.resultCopy}>
                        <Text style={styles.resultTitle}>{stop.type === "dropoff" ? "Customer drop-off" : `Pickup ${stop.sequence}`}</Text>
                        <Text style={styles.resultSub}>{stop.status} · {stop.latitude.toFixed(4)}, {stop.longitude.toFixed(4)}</Text>
                      </View>
                    </View>
                  ))}
                {activeStop ? (
                  <View style={styles.stopActions}>
                    {activeStop.status === "pending" ? (
                      <ActionButton
                        disabled={busy}
                        label="Confirm arrival"
                        onPress={() => void updateRouteStop(activeStop.id, "arrive")}
                      />
                    ) : (
                      <ActionButton
                        disabled={busy}
                        label={activeStop.type === "dropoff" ? "Confirm delivery" : "Confirm pickup"}
                        onPress={() => void updateRouteStop(activeStop.id, "complete")}
                      />
                    )}
                  </View>
                ) : (
                  <Text style={styles.tinyNote}>All route stops are complete.</Text>
                )}
                <Text style={styles.tinyNote}>Routing matrix · {route.routingProvider}</Text>
              </View>
            ) : null}
            <ActionButton
              disabled={tracking || !route || route.status !== "in_progress"}
              label={tracking ? "Background location sharing is active" : "Start background location sharing"}
              onPress={() => void beginTracking()}
            />
            {tracking ? <ActionButton label="Stop location sharing" onPress={() => void endTracking()} secondary /> : null}
          </View>
        ) : null}

        {role === "Merchant" ? (
          <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>MERCHANT / FULFILLMENT</Text>
              <Text style={styles.sectionTitle}>Prepare paid orders.</Text>
              <Text style={styles.helpText}>
                Review orders for your stores, accept them for preparation, and mark them ready for dispatch.
              </Text>
              <ActionButton
                disabled={busy}
                label="Refresh paid orders"
                onPress={() => void loadMerchantOrders()}
                secondary
              />
              {merchantOrders.length === 0 ? (
                <Text style={styles.tinyNote}>No paid orders awaiting fulfillment.</Text>
              ) : null}
              {merchantOrders.map((order) => (
                <View key={order.id} style={styles.merchantItem}>
                  <Text style={styles.resultTitle}>{order.storeName} · {order.status.replaceAll("_", " ")}</Text>
                  <Text style={styles.resultSub}>Order {order.id}</Text>
                  <Text style={styles.resultSub}>
                    Delivery window · {new Date(order.deliveryWindowStart).toLocaleString()} – {new Date(order.deliveryWindowEnd).toLocaleTimeString()}
                  </Text>
                  {order.items.map((item, index) => (
                    <Text key={`${order.id}-${index}`} style={styles.resultSub}>
                      {item.quantity} × {item.name} · ${(item.unitPriceCents / 100).toFixed(2)}
                    </Text>
                  ))}
                  {order.status === "placed" ? (
                    <ActionButton
                      disabled={busy}
                      label="Accept & start preparing"
                      onPress={() => void updateMerchantOrderState(order, "preparing")}
                    />
                  ) : null}
                  {order.status === "preparing" ? (
                    <ActionButton
                      disabled={busy}
                      label="Mark ready for dispatch"
                      onPress={() => void updateMerchantOrderState(order, "ready")}
                    />
                  ) : null}
                </View>
              ))}
            </View>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>MERCHANT / CATALOG</Text>
              <Text style={styles.sectionTitle}>Manage your store items.</Text>
              <Text style={styles.helpText}>
                Set prices and delivery volume. Items you mark unavailable disappear from customer catalogs; previous order prices remain unchanged.
              </Text>
              <ActionButton
                disabled={busy}
                label="Load my store locations"
                onPress={() => void loadMerchantStoreLocations()}
                secondary
              />
              {merchantStoreLocations.map((location) => (
                <Pressable
                  accessibilityRole="radio"
                  accessibilityState={{ selected: merchantStoreLocation === location.id }}
                  key={location.id}
                  onPress={() => setMerchantStoreLocation(location.id)}
                  style={[
                    styles.locationChoice,
                    merchantStoreLocation === location.id && styles.locationChoiceActive,
                  ]}
                >
                  <Text style={styles.resultTitle}>{location.name}</Text>
                  <Text style={styles.resultSub}>{location.id}</Text>
                </Pressable>
              ))}
              {merchantStoreLocations.length === 0 ? (
                <Text style={styles.tinyNote}>Load store locations before creating catalog items.</Text>
              ) : null}
              <Field label="Item name" onChangeText={setNewItemName} value={newItemName} />
              <Field label="Description" onChangeText={setNewItemDescription} value={newItemDescription} />
              <View style={styles.fieldGrid}>
                <Field label="Price (cents)" keyboardType="decimal-pad" onChangeText={setNewItemPrice} value={newItemPrice} />
                <Field label="Volume (liters)" keyboardType="decimal-pad" onChangeText={setNewItemVolume} value={newItemVolume} />
              </View>
              <ActionButton
                disabled={busy}
                label="Add catalog item"
                onPress={() => void createMerchantCatalogItem()}
              />
              <ActionButton
                disabled={busy}
                label="Refresh my catalog"
                onPress={() => void loadMerchantCatalog()}
                secondary
              />
              {merchantCatalog.length === 0 ? (
                <Text style={styles.tinyNote}>No catalog items loaded. Refresh to view items for your merchant account.</Text>
              ) : null}
              {merchantCatalog.map((item) => {
                const draft = merchantCatalogDrafts[item.id] ?? merchantCatalogDraft(item);
                return (
                  <View key={item.id} style={styles.merchantItem}>
                    <Text style={styles.resultSub}>Store · {item.storeLocationId}</Text>
                    <Field
                      label="Item name"
                      onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                        ...current, [item.id]: { ...current[item.id], name: value },
                      }))}
                      value={draft.name}
                    />
                    <Field
                      label="Description"
                      onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                        ...current, [item.id]: { ...current[item.id], description: value },
                      }))}
                      value={draft.description}
                    />
                    <View style={styles.fieldGrid}>
                      <Field
                        label="Price (cents)"
                        keyboardType="decimal-pad"
                        onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                          ...current, [item.id]: { ...current[item.id], unitPriceCents: value },
                        }))}
                        value={draft.unitPriceCents}
                      />
                      <Field
                        label="Volume (liters)"
                        keyboardType="decimal-pad"
                        onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                          ...current, [item.id]: { ...current[item.id], volumeLiters: value },
                        }))}
                        value={draft.volumeLiters}
                      />
                    </View>
                    <ActionButton
                      disabled={busy}
                      label="Save item changes"
                      onPress={() => void updateMerchantCatalogItem(item)}
                    />
                    <ActionButton
                      disabled={busy}
                      label={item.available ? "Hide from customer catalogs" : "Make available to customers"}
                      onPress={() => void toggleMerchantCatalogAvailability(item)}
                      secondary
                    />
                  </View>
                );
              })}
            </View>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>MERCHANT / SUBSCRIPTION</Text>
              <Text style={styles.sectionTitle}>Your storefront, in good standing.</Text>
              <Text style={styles.helpText}>
                Manage the flat $199 monthly ShopStops subscription. A successful invoice activates
                storefront visibility.
              </Text>
              <View style={styles.actionRow}>
                <ActionButton disabled={busy} label="Refresh billing status" onPress={() => void refreshSubscription()} />
                <ActionButton disabled={busy || !stripeKeyConfigured} label="Save payment method" onPress={() => void setupMerchantPaymentMethod()} secondary />
              </View>
              {subscription ? (
                <View style={styles.statusCard}>
                  <Text style={styles.statusWord}>{subscription.status.replaceAll("_", " ")}</Text>
                  <Text style={styles.resultSub}>
                    {subscription.storefrontVisible ? "Storefront visible" : "Storefront hidden"}
                    {" · "}
                    {subscription.paymentMethodReady ? "Payment method saved" : "Payment method needed"}
                  </Text>
                  {subscription.currentPeriodEnd ? (
                    <Text style={styles.resultSub}>Next billing period ends {new Date(subscription.currentPeriodEnd).toLocaleDateString()}</Text>
                  ) : null}
                </View>
              ) : null}
              <ActionButton
                disabled={busy || !subscription?.paymentMethodReady}
                label="Start $199 monthly subscription"
                onPress={() => void startSubscription()}
              />
              <Text style={styles.tinyNote}>USD · billed monthly through Stripe</Text>
            </View>
          </>
        ) : null}

        <Text style={styles.footer}>
          Securely connected through your identity gateway · ShopStops {new Date().getFullYear()}
        </Text>
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

function Field({
  label,
  value,
  onChangeText,
  keyboardType = "default",
  editable = true,
}: {
  label: string;
  value: string;
  onChangeText: (value: string) => void;
  keyboardType?: "default" | "decimal-pad";
  editable?: boolean;
}) {
  return (
    <View style={styles.field}>
      <Text style={styles.fieldLabel}>{label}</Text>
      <TextInput
        accessibilityLabel={label}
        autoCapitalize="none"
        autoCorrect={false}
        editable={editable}
        keyboardType={keyboardType}
        onChangeText={onChangeText}
        placeholder={label}
        placeholderTextColor="#94a3b8"
        style={styles.input}
        value={value}
      />
    </View>
  );
}

function ActionButton({
  label,
  onPress,
  disabled = false,
  secondary = false,
}: {
  label: string;
  onPress: () => void;
  disabled?: boolean;
  secondary?: boolean;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      disabled={disabled}
      onPress={onPress}
      style={[
        styles.actionButton,
        secondary && styles.actionButtonSecondary,
        disabled && styles.actionButtonDisabled,
      ]}
    >
      <Text style={[styles.actionText, secondary && styles.actionTextSecondary]}>{label}</Text>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  screen: { flex: 1, backgroundColor: "#f6f5f1" },
  content: { width: "100%", maxWidth: 760, alignSelf: "center", padding: 20, paddingBottom: 64 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 12, marginBottom: 18 },
  brandLockup: { flexDirection: "row", alignItems: "center", gap: 10 },
  brandIcon: { width: 42, height: 42, borderRadius: 12, backgroundColor: "#19352f" },
  brand: { color: "#19352f", fontSize: 21, fontWeight: "900", letterSpacing: -0.8 },
  tagline: { color: "#64756e", fontSize: 12, marginTop: 2 },
  livePill: { flexDirection: "row", alignItems: "center", borderRadius: 24, paddingHorizontal: 11, paddingVertical: 7, backgroundColor: "#e4efe8" },
  liveDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: "#398260", marginRight: 6 },
  liveText: { color: "#28694d", fontSize: 10, fontWeight: "800", letterSpacing: 1 },
  hero: { backgroundColor: "#19352f", borderRadius: 22, padding: 24, overflow: "hidden" },
  heroEyebrow: { color: "#f6a17e", fontSize: 10, fontWeight: "800", letterSpacing: 1.7 },
  heroTitle: { color: "#fffdf7", fontSize: 31, fontWeight: "800", lineHeight: 37, marginTop: 10, maxWidth: 430 },
  heroCopy: { color: "#c3d2cb", fontSize: 14, lineHeight: 21, marginTop: 8, maxWidth: 390 },
  heroStats: { flexDirection: "row", justifyContent: "space-between", marginTop: 24, paddingTop: 17, borderTopWidth: 1, borderTopColor: "#436057" },
  statValue: { color: "#fffdf7", fontSize: 19, fontWeight: "800" },
  statLabel: { color: "#afc4bb", fontSize: 10, marginTop: 3 },
  rolePicker: { flexDirection: "row", gap: 7, padding: 5, backgroundColor: "#e9e8e2", borderRadius: 14, marginTop: 18 },
  roleButton: { flex: 1, alignItems: "center", paddingVertical: 11, borderRadius: 10 },
  roleButtonActive: { backgroundColor: "#fff", elevation: 1 },
  roleText: { color: "#64756e", fontSize: 12, fontWeight: "700" },
  roleTextActive: { color: "#19352f" },
  connectionCard: { backgroundColor: "#fff", borderRadius: 16, padding: 15, marginTop: 14, borderWidth: 1, borderColor: "#ecece7" },
  connectionHeader: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  connectionCopy: { flex: 1 },
  connectionStatus: { color: "#19352f", fontSize: 14, fontWeight: "800", marginTop: 5 },
  connectionToggle: { paddingVertical: 9, paddingHorizontal: 13, backgroundColor: "#e9f0ec", borderRadius: 9 },
  connectionToggleText: { color: "#24443b", fontSize: 12, fontWeight: "800" },
  configCard: { backgroundColor: "#fff", borderRadius: 18, padding: 0, marginTop: 13 },
  card: { backgroundColor: "#fff", borderRadius: 18, padding: 20, marginTop: 14, borderWidth: 1, borderColor: "#ecece7", shadowColor: "#19352f", shadowOpacity: 0.04, shadowRadius: 12, shadowOffset: { width: 0, height: 4 }, elevation: 1 },
  sectionEyebrow: { color: "#e5653d", fontSize: 10, fontWeight: "800", letterSpacing: 1.4 },
  sectionTitle: { color: "#19352f", fontSize: 20, fontWeight: "800", marginTop: 7 },
  helpText: { color: "#65756f", fontSize: 13, lineHeight: 19, marginTop: 6, marginBottom: 11 },
  field: { flex: 1, minWidth: 125, marginTop: 6 },
  fieldLabel: { color: "#52645e", fontSize: 11, fontWeight: "700", marginBottom: 6 },
  fieldGrid: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  input: { backgroundColor: "#f8f9f7", borderColor: "#e4e9e5", borderWidth: 1, borderRadius: 10, color: "#203b34", fontSize: 13, paddingHorizontal: 12, paddingVertical: 11, marginTop: 8 },
  actionButton: { backgroundColor: "#e5653d", minHeight: 45, justifyContent: "center", alignItems: "center", borderRadius: 11, paddingHorizontal: 15, marginTop: 13 },
  actionButtonSecondary: { backgroundColor: "#e9f0ec" },
  actionButtonDisabled: { opacity: 0.48 },
  actionText: { color: "#fff", fontWeight: "800", fontSize: 12, textAlign: "center" },
  actionTextSecondary: { color: "#24443b" },
  actionRow: { gap: 3 },
  filterRow: { flexDirection: "row", flexWrap: "wrap", gap: 7, marginTop: 12 },
  filterChip: { borderWidth: 1, borderColor: "#dfe7e1", backgroundColor: "#f8faf8", borderRadius: 18, paddingHorizontal: 12, paddingVertical: 8 },
  filterChipActive: { borderColor: "#397457", backgroundColor: "#e7f1eb" },
  filterChipText: { color: "#64756e", fontSize: 11, fontWeight: "700" },
  filterChipTextActive: { color: "#24443b" },
  messageBox: { padding: 13, borderRadius: 11, backgroundColor: "#fff1e9", marginTop: 12 },
  messageText: { color: "#8e4229", fontSize: 12, lineHeight: 18 },
  loader: { marginTop: 12 },
  resultRow: { flexDirection: "row", alignItems: "center", paddingVertical: 12, borderTopWidth: 1, borderTopColor: "#edf0ed", marginTop: 12 },
  resultIcon: { width: 38, height: 38, borderRadius: 12, backgroundColor: "#e7f1eb", justifyContent: "center", alignItems: "center", marginRight: 11 },
  resultIconText: { color: "#397457", fontSize: 22, fontWeight: "500" },
  resultCopy: { flex: 1 },
  resultTitle: { color: "#213b34", fontWeight: "800", fontSize: 13 },
  resultSub: { color: "#73817b", fontSize: 11, marginTop: 4 },
  catalogRow: { flexDirection: "row", alignItems: "center", paddingVertical: 12, borderTopWidth: 1, borderTopColor: "#edf0ed", marginTop: 10 },
  merchantItem: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 14 },
  locationChoice: { borderWidth: 1, borderColor: "#e4e9e5", borderRadius: 10, padding: 11, marginTop: 8 },
  locationChoiceActive: { borderColor: "#397457", backgroundColor: "#e7f1eb" },
  quantityControl: { flexDirection: "row", alignItems: "center", gap: 10, marginLeft: 10 },
  quantityButton: { width: 30, height: 30, borderRadius: 15, backgroundColor: "#e9f0ec", alignItems: "center", justifyContent: "center" },
  quantityButtonText: { color: "#24443b", fontSize: 17, fontWeight: "800" },
  quantityText: { minWidth: 16, color: "#213b34", textAlign: "center", fontWeight: "800" },
  checkoutBox: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 16 },
  orderProgressCard: { backgroundColor: "#f5f8f5", borderRadius: 12, padding: 13, marginTop: 12, borderWidth: 1, borderColor: "#e3ebe5" },
  orderProgressHeader: { flexDirection: "row", justifyContent: "space-between", alignItems: "center" },
  orderProgressStatus: { color: "#24483a", fontSize: 13, fontWeight: "800", textTransform: "capitalize" },
  orderProgressLive: { color: "#398260", fontSize: 9, fontWeight: "800", letterSpacing: 1 },
  orderProgressEvent: { color: "#607268", fontSize: 10, marginTop: 6, textTransform: "capitalize" },
  orderProgressError: { color: "#9a493c", fontSize: 10, marginTop: 6 },
  routeBox: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 14 },
  stopActions: { marginTop: 3 },
  checkoutLabel: { color: "#77857f", fontSize: 9, fontWeight: "800", letterSpacing: 1.2, marginBottom: 11 },
  invoiceRow: { flexDirection: "row", justifyContent: "space-between", paddingVertical: 7 },
  invoiceLabel: { color: "#5f706a", fontSize: 12, textTransform: "capitalize" },
  invoiceAmount: { color: "#213b34", fontSize: 12, fontWeight: "700" },
  totalRow: { borderTopWidth: 1, borderTopColor: "#dfe6e1", marginTop: 4, paddingTop: 11 },
  totalLabel: { color: "#203b34", fontWeight: "800", fontSize: 13 },
  totalValue: { color: "#203b34", fontWeight: "900", fontSize: 15 },
  tinyNote: { color: "#829089", fontSize: 10, marginTop: 9, lineHeight: 15 },
  stopRow: { flexDirection: "row", alignItems: "center", paddingVertical: 9 },
  stopNumber: { width: 28, height: 28, borderRadius: 14, backgroundColor: "#e5653d", justifyContent: "center", alignItems: "center", marginRight: 11 },
  stopNumberLast: { backgroundColor: "#397457" },
  stopNumberText: { color: "#fff", fontWeight: "800", fontSize: 11 },
  statusCard: { backgroundColor: "#f8f9f7", padding: 15, borderRadius: 12, marginTop: 15 },
  statusWord: { color: "#24443b", fontWeight: "900", fontSize: 18, textTransform: "capitalize", marginBottom: 5 },
  footer: { color: "#8b9892", textAlign: "center", fontSize: 10, marginTop: 25, lineHeight: 15 },
});
