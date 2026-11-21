import * as Location from "expo-location";
import { useStripe } from "@stripe/stripe-react-native";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  Image,
  Alert,
  AppState,
  ActivityIndicator,
  KeyboardAvoidingView,
  Linking,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { ApiError, apiRequest, idempotencyKey, RequestScope, type ApiSettings } from "../src/api";
import { ConnectionPanel } from "../src/ConnectionPanel";
import { mobileConfiguration } from "../src/config";
import { LocationSharing } from "../src/locationSharing";
import { isTestPublishableKey, priorityPricing, type PriorityPricing } from "../src/priority";
import { ActionButton, Field, StateCard } from '../src/WorkspaceUI';
import { cartSummary, customerStages, defaultDeliveryWindow, definiteRejection, deliveryCoordinates, deliveryWindow as validateWindow, merchantCatalogValues, money, nextRouteStop, readableDate, requiredUUID, retainAttempt, routeCanProgress, type CustomerStage } from '../src/workflow';

type Role = "Customer" | "Driver" | "Merchant";
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
type SavedOrder = { id: string; status: string; itemSubtotalCents: number; storeLocationId: string; itemCount: number };
type Checkout = {
  priority?: { selected: boolean; testOnly: boolean };
  id: string;
  status: string;
  currency: string;
  totalCents: number;
  invoiceLines: { type: string; merchantId?: string; amountCents: number }[];
};
type Route = {
  id: string;
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
type Subscription = {
  status: string;
  monthlyAmountCents: number;
  currency: string;
  storefrontVisible: boolean;
  currentPeriodEnd: string | null;
  paymentMethodReady: boolean;
};

const stripeKeyConfigured = Boolean(process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY);
const configuration = mobileConfiguration(process.env.EXPO_PUBLIC_API_URL, __DEV__, process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY);
type Connection = { gatewayUrl: string; sessionToken: string; role: Role; revision: number; scope: RequestScope };

export default function HomeScreen() {
  const [connection, setConnection] = useState<Connection>(() => ({ gatewayUrl: configuration.baseUrl, sessionToken: '', role: 'Customer', revision: 0, scope: new RequestScope() }));
  const [editing, setEditing] = useState(false);
  function change(update: Partial<Pick<Connection, 'gatewayUrl' | 'sessionToken' | 'role'>>) {
    connection.scope.close(); // synchronous: no old request may win this transition
    setConnection(current => ({ ...current, ...update, revision: current.revision + 1, scope: new RequestScope() }));
  }
  if (configuration.error) return <View style={{ padding: 32, paddingTop: 72 }}><Text accessibilityRole="alert">{configuration.error}</Text></View>;
  return <>
    <HomeWorkspace key={connection.revision} connection={connection} onRole={role => change({ role })} onEdit={() => setEditing(true)} onClear={() => change({ sessionToken: '' })} />
    <ConnectionPanel visible={editing} gatewayUrl={connection.gatewayUrl} sessionToken={connection.sessionToken} onGateway={gatewayUrl => change({ gatewayUrl })} onToken={sessionToken => change({ sessionToken })} onClose={() => setEditing(false)} onClear={() => change({ sessionToken: '' })} />
  </>;
}

function HomeWorkspace({ connection, onRole, onEdit, onClear }: { connection: Connection; onRole: (role: Role) => void; onEdit: () => void; onClear: () => void }) {
  const { role, gatewayUrl, sessionToken } = connection;
  const [connectionOpen, setConnectionOpen] = useState(false);
  const [stage, setStage] = useState<CustomerStage>('Browse');
  const [advancedOrders, setAdvancedOrders] = useState(false);
  const [catalogLoaded, setCatalogLoaded] = useState(false);
  const [nearbyLoaded, setNearbyLoaded] = useState(false);
  const [merchantCatalogLoaded, setMerchantCatalogLoaded] = useState(false);
  const [locationsLoaded, setLocationsLoaded] = useState(false);
  useLayoutEffect(() => {
    if (connection.scope.signal.aborted) connection.scope = new RequestScope();
    return () => connection.scope.close();
  }, [connection]);
  const settings = useMemo<ApiSettings>(
    () => ({ baseUrl: gatewayUrl, sessionToken, get signal() { return connection.scope.signal; }, allowLocal: __DEV__ }),
    [gatewayUrl, sessionToken, connection],
  );
  const [requestBusy, setBusy] = useState(false);
  const [paymentActive, setPaymentActive] = useState(false);
  const [trackingStarting, setTrackingStarting] = useState(false);
  const busy = requestBusy || paymentActive;
  const [message, setMessage] = useState("");
  const [nearby, setNearby] = useState<NearbyMerchant[]>([]);
  const [catalog, setCatalog] = useState<CatalogItem[]>([]);
  const [catalogStoreID, setCatalogStoreID] = useState("");
  const [quantities, setQuantities] = useState<Record<string, number>>({});
  const [checkout, setCheckout] = useState<Checkout | null>(null);
  const [savedOrders, setSavedOrders] = useState<SavedOrder[]>([]);
  const [paymentState, setPaymentState] = useState<'not_started' | 'submitted' | 'uncertain'>('not_started');
  const [priorityPrice, setPriorityPrice] = useState<PriorityPricing | null>(null);
  const [prioritySelected, setPrioritySelected] = useState(false);
  const [pricingRefresh, setPricingRefresh] = useState(0);
  const [route, setRoute] = useState<Route | null>(null);
  const [subscription, setSubscription] = useState<Subscription | null>(null);
  const [merchantStoreLocations, setMerchantStoreLocations] = useState<MerchantStoreLocation[]>([]);
  const [merchantCatalog, setMerchantCatalog] = useState<MerchantCatalogItem[]>([]);
  const [merchantCatalogDrafts, setMerchantCatalogDrafts] = useState<Record<string, MerchantCatalogDraft>>({});
  const [merchantStoreLocation, setMerchantStoreLocation] = useState("");
  const [newItemName, setNewItemName] = useState("");
  const [newItemDescription, setNewItemDescription] = useState("");
  const [newItemPrice, setNewItemPrice] = useState("");
  const [newItemVolume, setNewItemVolume] = useState("");
  const [anchorStore, setAnchorStore] = useState("");
  const [orderIds, setOrderIds] = useState("");
  const [latitude, setLatitude] = useState("");
  const [longitude, setLongitude] = useState("");
  const [routeId, setRouteId] = useState("");
  const [tracking, setTracking] = useState(false);
  const [lastLocationReport, setLastLocationReport] = useState<string | null>(null);
  const [locationError, setLocationError] = useState('');
  const [connectionStatus, setConnectionStatus] = useState('Not verified · send a request to check access');
  const inFlight = useRef(false);
  const paymentBusy = useRef(false);
  const locationSharing = useRef(new LocationSharing<Location.LocationObject>());
  const stopAttempt = useRef<{ fingerprint: string; body: string } | null>(null);
  const setupAttempt = useRef<string | null>(null);
  const [deliveryWindow, setDeliveryWindow] = useState(defaultDeliveryWindow);
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
  const checkoutAttempt = useRef<{
    key: string;
    fingerprint: string;
    body: {
      priorityService: boolean;
      anchorStoreLocationId: string;
      orderIds: string[];
      dropoff: { latitude: number; longitude: number };
      deliveryWindowStart: string;
      deliveryWindowEnd: string;
    };
  } | null>(null);
  const { initPaymentSheet, presentPaymentSheet } = useStripe();

  useEffect(() => {
    let active = true;
    setPriorityPrice(null);
    setPrioritySelected(false);
    if (role !== 'Customer' || !gatewayUrl || !sessionToken) return;
    // Keep an unresolved checkout attempt intact: its idempotency key and
    // original choice must not be silently changed during a pricing refresh.
    void apiRequest<unknown>(settings, "checkout-pricing/priority")
      .then((value) => { if (active) setPriorityPrice(priorityPricing(value)); })
      .catch(() => { if (active) setPriorityPrice(null); });
    return () => { active = false; };
  }, [settings, pricingRefresh, role, gatewayUrl, sessionToken]);

  const run = useCallback(async <T,>(action: () => Promise<T>, onRejected?: () => void) => {
    if (inFlight.current) return undefined;
    inFlight.current = true;
    const scope = connection.scope;
    setBusy(true);
    setMessage("");
    try {
      scope.assertCurrent();
      const result = await action();
      scope.assertCurrent();
      setConnectionStatus('Gateway responded · permissions checked per action');
      return result;
    } catch (error) {
      if (!scope.signal.aborted) {
        if (error instanceof ApiError && definiteRejection(error)) onRejected?.();
        setMessage(error instanceof ApiError ? error.message : "The request could not be completed.");
        setConnectionStatus(error instanceof ApiError && error.status ? `Gateway rejected request (${error.status})` : 'Gateway unavailable or request invalid');
      }
      return undefined;
    } finally {
      if (!scope.signal.aborted) { inFlight.current = false; setBusy(false); }
    }
  }, [connection]);

  useEffect(() => {
    const sharing = locationSharing.current;
    const signal = connection.scope.signal;
    const stop = () => sharing.stop();
    signal.addEventListener('abort', stop, { once: true });
    return () => {
      signal.removeEventListener('abort', stop);
      sharing.stop();
    };
  }, [connection]);

  useEffect(() => {
    const listener = AppState.addEventListener('change', state => {
      if (state !== 'active') endTracking();
    });
    return () => listener.remove();
  }, []);

  useEffect(() => {
    if (!tracking || role !== 'Driver') return;
    let active = true;
    const sharing = locationSharing.current;
    const timer = setInterval(() => {
      if (!route) return;
      void sharing.report(async (current, signal) => {
        await apiRequest(
        { ...settings, signal },
        `driver/routes/${encodeURIComponent(route.id)}/location`,
        {
          method: "POST",
          body: JSON.stringify({
            eventId: `gps-${current.timestamp}`,
            observedAt: new Date(current.timestamp).toISOString(),
            location: {
              latitude: current.coords.latitude,
              longitude: current.coords.longitude,
            },
          }),
        },
        );
        if (active && !signal.aborted && !settings.signal?.aborted) {
          setLastLocationReport(new Date(current.timestamp).toISOString());
          setLocationError('');
        }
      }).catch((error: unknown) => {
        if (active && !settings.signal?.aborted) setLocationError(error instanceof ApiError ? error.message : 'GPS update failed.');
      });
    }, 5000);
    return () => { active = false; clearInterval(timer); };
  }, [route?.id, settings, tracking, role]);

  async function findNearby() {
    try { requiredUUID(anchorStore, 'anchor store'); } catch (error) { setMessage((error as Error).message); return; }
    const results = await run(() =>
      apiRequest<NearbyMerchant[]>(
        settings,
        `stores/${encodeURIComponent(anchorStore)}/nearby-merchants?radiusMeters=500`,
      ),
    );
    if (results) { setNearbyLoaded(true); setNearby(results); }
  }

  async function loadCatalog(storeID = catalogStoreID) {
    if (orderAttempt.current || checkoutAttempt.current) {
      setMessage('Retry the unchanged request or reconcile it with support before changing stores.');
      return;
    }
    const normalizedID = storeID.trim();
    if (!normalizedID) {
      setMessage("Enter a store location ID to browse its catalog.");
      return;
    }
    try { requiredUUID(normalizedID, 'store location'); } catch (error) { setMessage((error as Error).message); return; }
    const results = await run(() =>
      apiRequest<CatalogItem[]>(
        settings,
        `stores/${encodeURIComponent(normalizedID)}/catalog`,
      ),
    );
    if (results) {
      setCatalogStoreID(normalizedID);
      setCatalogLoaded(true);
      setCatalog(results);
      setQuantities({});
      setStage('Cart');
    }
  }

  async function createCustomerOrder() {
    if (inFlight.current || paymentBusy.current) return;
    if (checkoutAttempt.current) { setMessage('Reconcile the pending basket before adding orders.'); return; }
    if (checkout) {
      setMessage("Start a new basket before creating another order.");
      return;
    }
    try { requiredUUID(catalogStoreID, 'store location'); deliveryCoordinates(latitude, longitude); } catch (error) { setMessage((error as Error).message); return; }
    const lat = Number(latitude);
    const lon = Number(longitude);
    const items = catalog
      .filter((item) => (quantities[item.id] ?? 0) > 0)
      .map((item) => ({ catalogItemId: item.id, quantity: quantities[item.id] }));
    if (
      !latitude.trim() || !longitude.trim() || !catalogStoreID.trim() || !Number.isFinite(lat) || lat < -90 || lat > 90 ||
      !Number.isFinite(lon) || lon < -180 || lon > 180 || items.length === 0
    ) {
      setMessage("Choose catalog items and enter valid delivery coordinates.");
      return;
    }
    const body: CreateOrderBody = {
      storeLocationId: catalogStoreID.trim(),
      dropoff: { latitude: lat, longitude: lon },
      deliveryWindowStart: deliveryWindow.start,
      deliveryWindowEnd: deliveryWindow.end,
      items,
    };
    try {
      // An unchanged retry must work even if its original requested window has elapsed.
      if (!orderAttempt.current) validateWindow(deliveryWindow.start, deliveryWindow.end);
      orderAttempt.current = retainAttempt(orderAttempt.current, body, idempotencyKey);
    } catch (error) { setMessage((error as Error).message); return; }
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
      () => { orderAttempt.current = null; },
    );
    if (result) {
      orderAttempt.current = null;
      checkoutAttempt.current = null;
      const currentIDs = orderIds.split(/[\s,]+/).map((id) => id.trim()).filter(Boolean);
      if (!currentIDs.includes(result.id)) {
        setOrderIds([...currentIDs, result.id].join(", "));
      }
      setSavedOrders(current => [...current.filter(order => order.id !== result.id), {
        ...result, storeLocationId: attempt.body.storeLocationId,
        itemCount: attempt.body.items.reduce((total, item) => total + item.quantity, 0),
      }]);
      setQuantities({});
      setStage('Checkout');
      setMessage(`Order created · ${money(result.itemSubtotalCents)} items. Delivery and fees are calculated in checkout.`);
    }
  }

  async function createCheckout() {
    if (inFlight.current || paymentBusy.current || checkout) return;
    if (orderAttempt.current) { setMessage('Retry or reconcile the pending order before creating a basket.'); return; }
    const ids = orderIds
      .split(/[\s,]+/)
      .map((id) => id.trim())
      .filter(Boolean);
    try {
      requiredUUID(anchorStore, 'anchor store');
      if (!ids.length) throw new Error('Create an order from your cart first.');
      ids.forEach(id => requiredUUID(id, 'order'));
      deliveryCoordinates(latitude, longitude);
    } catch (error) { setMessage((error as Error).message); return; }
    const lat = Number(latitude);
    const lon = Number(longitude);
    if (!latitude.trim() || !longitude.trim() || !anchorStore.trim() || ids.length === 0 || !Number.isFinite(lat) || lat < -90 || lat > 90 || !Number.isFinite(lon) || lon < -180 || lon > 180) {
      setMessage("Enter the anchor store, order IDs, and delivery coordinates.");
      return;
    }
    try {
      checkoutAttempt.current = retainAttempt(checkoutAttempt.current, {
          priorityService: prioritySelected && priorityPrice !== null,
          anchorStoreLocationId: anchorStore.trim(),
          orderIds: ids,
          dropoff: { latitude: lat, longitude: lon },
          deliveryWindowStart: deliveryWindow.start,
          deliveryWindowEnd: deliveryWindow.end,
        }, idempotencyKey);
    } catch (error) { setMessage((error as Error).message); return; }
    const attempt = checkoutAttempt.current;
    const result = await run(() =>
      apiRequest<Checkout>(settings, "checkouts", {
        method: "POST",
        headers: { "Idempotency-Key": attempt.key },
        body: JSON.stringify(attempt.body),
      }),
      () => { checkoutAttempt.current = null; },
    );
    if (result) {
      checkoutAttempt.current = null;
      setCheckout(result);
    }
  }

  async function payCheckout() {
    if (paymentBusy.current || inFlight.current || paymentState !== 'not_started') return;
    paymentBusy.current = true;
    setPaymentActive(true);
    try {
    if (!checkout || !stripeKeyConfigured) return;
    if (checkout.priority?.testOnly && !isTestPublishableKey(process.env.EXPO_PUBLIC_STRIPE_PUBLISHABLE_KEY)) {
      setMessage("This test-only priority basket requires a Stripe test publishable key.");
      return;
    }
    setPaymentState('uncertain');
    const intent = await run(() =>
      apiRequest<{ clientSecret: string; amountCents: number; currency: string }>(
        settings,
        `checkouts/${encodeURIComponent(checkout.id)}/payment`,
        { method: "POST" },
      ),
      () => setPaymentState('not_started'),
    );
    if (!intent) return;
    if (intent.amountCents !== checkout.totalCents || intent.currency !== checkout.currency) {
      setMessage("Payment total does not match the basket. Reconciliation is required.");
      return;
    }
    const initialized = await initPaymentSheet({
      merchantDisplayName: "ShopStops",
      paymentIntentClientSecret: intent.clientSecret,
    });
    if (connection.scope.signal.aborted) return;
    if (initialized.error) {
      setMessage('Payment could not be initialized. Check the basket before retrying.');
      return;
    }
    const presented = await presentPaymentSheet();
    if (connection.scope.signal.aborted) return;
    if (presented.error?.code === 'Canceled') {
      setPaymentState('not_started');
      setMessage('Payment sheet closed. No payment confirmation was received.');
    } else if (presented.error) {
      setMessage('Payment outcome is uncertain. Reconcile this basket with support before trying again.');
    } else {
      setPaymentState('submitted');
      setStage('Status');
      setMessage('Payment submitted, not yet confirmed. Keep the basket ID for support.');
    }
    } catch { if (!connection.scope.signal.aborted) setMessage('Payment could not be completed. Check payment status before retrying.'); }
    finally { paymentBusy.current = false; if (!connection.scope.signal.aborted) setPaymentActive(false); }
  }

  function startNewBasket() {
    if (busy || orderAttempt.current || checkoutAttempt.current) return;
    Alert.alert('Clear this local basket?', 'This does not cancel saved orders, a basket, or a payment on the server. Keep your IDs and reconcile any submitted or uncertain payment with support before starting another basket.', [
      { text: 'Keep basket', style: 'cancel' },
      { text: 'I have reconciled · clear locally', style: 'destructive', onPress: () => {
        setCheckout(null); setSavedOrders([]); setOrderIds(''); setQuantities({});
        setCatalog([]); setCatalogLoaded(false); setCatalogStoreID(''); setAnchorStore('');
        setNearby([]); setNearbyLoaded(false); setLatitude(''); setLongitude('');
        setPaymentState('not_started'); setPrioritySelected(false);
        setDeliveryWindow(defaultDeliveryWindow()); setStage('Browse');
        setMessage('Local basket cleared. No server order or payment was cancelled.');
      } },
    ]);
  }

  async function loadRoute() {
    try { requiredUUID(routeId, 'assigned route'); } catch (error) { setMessage((error as Error).message); return; }
    if (!routeId.trim()) {
      setMessage("Enter your assigned route ID.");
      return;
    }
    const result = await run(() =>
      apiRequest<Route>(settings, `driver/routes/${encodeURIComponent(routeId.trim())}`),
    );
    if (result) {
      setRoute(result);
      // A server refresh reconciles a previous stop write without inventing progress.
      if (stopAttempt.current) {
        const event = JSON.parse(stopAttempt.current.body) as { type: string };
        const stop = result.stops.find(entry => JSON.stringify([result.id, entry.id, event.type]) === stopAttempt.current?.fingerprint);
        if (stop && (stop.status === 'completed' || (event.type === 'arrive' && stop.status === 'arrived'))) stopAttempt.current = null;
      }
      if (result.status !== 'in_progress') stopAttempt.current = null;
      if (!routeCanProgress(result)) endTracking();
    }
  }

  async function updateRouteStop(stopId: string, type: "arrive" | "complete") {
    if (inFlight.current || !routeCanProgress(route) || !route) return;
    const next = nextRouteStop(route.stops);
    if (next?.id !== stopId || (type === 'arrive' ? next.status !== 'pending' : next.status !== 'arrived')) return;
    const routeKey = route.id;
    const fingerprint = JSON.stringify([routeKey, stopId, type]);
    if (stopAttempt.current && stopAttempt.current.fingerprint !== fingerprint) {
      setMessage('Refresh the route to reconcile the previous stop event before submitting another.');
      return;
    }
    if (stopAttempt.current?.fingerprint !== fingerprint) stopAttempt.current = {
      fingerprint, body: JSON.stringify({ eventId: idempotencyKey(), type, observedAt: new Date().toISOString() }),
    };
    const attempt = stopAttempt.current;
    const result = await run(() =>
      apiRequest<StopEvent>(
        settings,
        `driver/routes/${encodeURIComponent(routeKey)}/stops/${encodeURIComponent(stopId)}/events`,
        {
          method: "POST",
          body: attempt.body,
        },
      ),
      () => { stopAttempt.current = null; },
    );
    if (result) {
      stopAttempt.current = null;
      if (result.routeStatus !== 'in_progress') endTracking();
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
    }
  }

  async function beginTracking() {
    if (trackingStarting || locationSharing.current.active) return;
    if (!routeCanProgress(route) || role !== 'Driver' || !routeId.trim()) {
      setMessage('Load an in-progress assigned route before starting location sharing.');
      return;
    }
    setTrackingStarting(true);
    setLastLocationReport(null);
    setLocationError('');
    try {
    const started = await locationSharing.current.start(async (current, receive) => {
      const permission = await Location.requestForegroundPermissionsAsync();
      if (!current() || connection.scope.signal.aborted || AppState.currentState !== 'active') return null;
      if (permission.status !== "granted") {
        setMessage("Allow location access while using the app to share your active route.");
        return null;
      }
      const position = await Location.getCurrentPositionAsync({
        accuracy: Location.Accuracy.Balanced,
      });
      if (!current() || connection.scope.signal.aborted || AppState.currentState !== 'active') return null;
      receive(position);
      return Location.watchPositionAsync(
        {
          accuracy: Location.Accuracy.Balanced,
          timeInterval: 5000,
          distanceInterval: 0,
        },
        receive,
      );
    });
      if (connection.scope.signal.aborted) return;
      setTracking(started);
      if (started) setMessage("Location sharing is active while this screen remains open.");
    } catch {
      if (!connection.scope.signal.aborted) setMessage("Could not start location updates. Check device location services.");
    } finally { if (!connection.scope.signal.aborted) setTrackingStarting(false); }
  }

  function endTracking() {
    locationSharing.current.stop();
    setTrackingStarting(false);
    setTracking(false);
    setMessage("Location sharing stopped.");
  }

  async function openDirections() {
    const stop = route && nextRouteStop(route.stops);
    if (!stop || !Number.isFinite(stop.latitude) || !Number.isFinite(stop.longitude) ||
        Math.abs(stop.latitude) > 90 || Math.abs(stop.longitude) > 180) return;
    try {
      await Linking.openURL(`https://www.google.com/maps/dir/?api=1&destination=${stop.latitude},${stop.longitude}&travelmode=driving`);
    } catch { if (!connection.scope.signal.aborted) setMessage('Could not open directions. Use the displayed stop coordinates in your navigation app.'); }
  }

  useEffect(() => {
    if ((role !== "Driver" || !settings.sessionToken.trim()) && tracking) endTracking();
  }, [role, settings.sessionToken, tracking]);

  async function refreshSubscription() {
    const result = await run(() => apiRequest<Subscription>(settings, "merchant/subscription"));
    if (result) setSubscription(result);
    return result;
  }

  async function setupMerchantPaymentMethod() {
    if (paymentBusy.current || inFlight.current) return;
    paymentBusy.current = true;
    setPaymentActive(true);
    try {
    setupAttempt.current ??= idempotencyKey();
    const intent = await run(() =>
      apiRequest<{ clientSecret: string }>(settings, "merchant/subscription/setup-intent", {
        method: "POST",
        headers: { "Idempotency-Key": setupAttempt.current! },
      }),
    );
    if (!intent) return;
    const initialized = await initPaymentSheet({
      merchantDisplayName: "ShopStops Merchant",
      setupIntentClientSecret: intent.clientSecret,
    });
    if (connection.scope.signal.aborted) return;
    setupAttempt.current = null;
    if (initialized.error) {
      setMessage('Billing setup could not be initialized. Refresh billing status before retrying.');
      return;
    }
    const presented = await presentPaymentSheet();
    if (connection.scope.signal.aborted) return;
    if (presented.error) {
      setMessage('Billing setup was cancelled or unsuccessful. Refresh billing status before retrying.');
      return;
    }
    const refreshed = await refreshSubscription();
    if (refreshed) setMessage('Billing setup submitted. The refreshed server status below determines whether your payment method is ready.');
    } catch { if (!connection.scope.signal.aborted) setMessage('Billing setup failed. Refresh billing status before retrying.'); }
    finally { paymentBusy.current = false; if (!connection.scope.signal.aborted) setPaymentActive(false); }
  }

  async function startSubscription() {
    const result = await run(() =>
      apiRequest<{ status: string }>(settings, "merchant/subscription", { method: "POST" }),
    );
    if (result) {
      const refreshed = await refreshSubscription();
      if (refreshed) setMessage(`Subscription is ${refreshed.status}. Storefront activation waits for a paid invoice.`);
    }
  }

  async function loadMerchantCatalog() {
    const items = await run(() =>
      apiRequest<MerchantCatalogItem[]>(settings, "merchant/catalog"),
    );
    if (items) {
      setMerchantCatalogLoaded(true);
      setMerchantCatalog(items);
      setMerchantCatalogDrafts(Object.fromEntries(items.map((item) => [
        item.id,
        {
          name: item.name,
          description: item.description,
          unitPriceCents: String(item.unitPriceCents),
          volumeLiters: item.volumeLiters,
        },
      ])));
    }
  }

  async function loadMerchantStoreLocations() {
    const locations = await run(() =>
      apiRequest<MerchantStoreLocation[]>(settings, "merchant/store-locations"),
    );
    if (locations) {
      setLocationsLoaded(true);
      setMerchantStoreLocations(locations);
      if (!merchantStoreLocation && locations.length > 0) {
        setMerchantStoreLocation(locations[0].id);
      }
    }
  }

  async function createMerchantCatalogItem() {
    if (inFlight.current || paymentBusy.current) return;
    try {
      const body = {
        storeLocationId: requiredUUID(merchantStoreLocation, 'store location'),
        ...merchantCatalogValues({ name: newItemName, description: newItemDescription, unitPriceCents: newItemPrice, volumeLiters: newItemVolume }),
      };
      merchantCatalogAttempt.current = retainAttempt(merchantCatalogAttempt.current, body, idempotencyKey);
    } catch (error) { setMessage((error as Error).message); return; }
    const attempt = merchantCatalogAttempt.current;
    const item = await run(() =>
      apiRequest<MerchantCatalogItem>(settings, "merchant/catalog", {
        method: "POST",
        headers: { "Idempotency-Key": attempt.key },
        body: JSON.stringify(attempt.body),
      }),
      () => { merchantCatalogAttempt.current = null; },
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
    let values: ReturnType<typeof merchantCatalogValues>;
    try { values = merchantCatalogValues(draft); }
    catch (error) { setMessage((error as Error).message); return; }
    const updated = await run(() =>
      apiRequest<MerchantCatalogItem>(
        settings,
        `merchant/catalog/${encodeURIComponent(item.id)}`,
        {
          method: "PATCH",
          body: JSON.stringify({
            ...values,
            available: item.available,
          }),
        },
      ),
    );
    if (updated) {
      setMerchantCatalog((current) => current.map((entry) =>
        entry.id === updated.id ? updated : entry,
      ));
      setMerchantCatalogDrafts(current => ({ ...current, [updated.id]: {
        name: updated.name, description: updated.description,
        unitPriceCents: String(updated.unitPriceCents), volumeLiters: updated.volumeLiters,
      } }));
      setMessage("Catalog item updated.");
    }
  }

  async function toggleMerchantCatalogAvailability(item: MerchantCatalogItem) {
    const draft = merchantCatalogDrafts[item.id];
    if (!draft) return;
    const updated = await run(() =>
      apiRequest<MerchantCatalogItem>(
        settings,
        `merchant/catalog/${encodeURIComponent(item.id)}`,
        {
          method: "PATCH",
          body: JSON.stringify({
            name: item.name,
            description: item.description,
            unitPriceCents: item.unitPriceCents,
            volumeLiters: Number(item.volumeLiters),
            available: !item.available,
          }),
        },
      ),
    );
    if (updated) {
      setMerchantCatalog((current) => current.map((entry) =>
        entry.id === updated.id ? updated : entry,
      ));
    }
  }

  const sharedForm = (
    <View style={styles.configCard}>
      <Text style={styles.sectionEyebrow}>SECURE CONNECTION</Text>
      <Text style={styles.sectionTitle}>Connect to your ShopStops gateway</Text>
      <Text style={styles.helpText}>
        Use the public HTTPS gateway and a user session from your identity provider. Never enter
        the backend service token here.
      </Text>
      <Text style={styles.helpText}>{connectionStatus}</Text>
      <Text style={styles.tinyNote}>Credentials stay in memory. Role selection does not grant permissions. No integrated SSO yet.</Text>
      <ActionButton label="Edit gateway and session" onPress={onEdit} secondary />
      <ActionButton label="Clear session and private records" onPress={onClear} secondary />
    </View>
  );
  const activeStop = route ? nextRouteStop(route.stops) : undefined;
  const orderLocked = busy || Boolean(checkout) || Boolean(orderAttempt.current) || Boolean(checkoutAttempt.current);
  const cart = cartSummary(catalog, quantities);
  const orderCount = orderIds.split(/[\s,]+/).filter(Boolean).length;

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
          <Text style={styles.heroEyebrow}>{role.toUpperCase()} WORKSPACE</Text>
          <Text style={styles.heroTitle}>{role === 'Customer' ? 'Your neighborhood, on the way.' : role === 'Driver' ? 'A clear next stop.' : 'Your store, ready for discovery.'}</Text>
          <Text style={styles.heroCopy}>
            {role === 'Customer' ? 'Browse a local store, build your order, then review the real checkout total.' : role === 'Driver' ? 'Open the route provided by dispatch. Confirm arrival and handoff in server-assigned order.' : 'Choose your location, publish a catalog, and check billing before opening your storefront.'}
          </Text>
        </View>

        <View style={styles.rolePicker}>
          {(["Customer", "Driver", "Merchant"] as const).map((item) => (
            <Pressable
              accessibilityRole="tab"
              accessibilityState={{ selected: role === item }}
              key={item}
              onPress={() => {
                if (item !== role) onRole(item);
              }}
              style={[styles.roleButton, role === item && styles.roleButtonActive]}
            >
              <Text style={[styles.roleText, role === item && styles.roleTextActive]}>{item}</Text>
            </Pressable>
          ))}
        </View>

        <Text style={styles.tinyNote}>Worker shopping: managed by partner store; not available in this app. Supervised records are reviewed by authorized admins. Changing roles clears this workspace; roles do not grant access.</Text>

        <View style={styles.connectionCard}>
          <View style={styles.connectionHeader}>
            <View style={styles.connectionCopy}>
              <Text style={styles.sectionEyebrow}>OPERATOR / BETA SETUP</Text>
              <Text style={styles.connectionStatus}>
                {gatewayUrl && sessionToken ? connectionStatus : "Not connected · configure gateway and session"}
              </Text>
            </View>
            <Pressable
              accessibilityRole="button"
              accessibilityState={{ expanded: connectionOpen }}
              onPress={() => setConnectionOpen((open) => !open)}
              style={styles.connectionToggle}
            >
              <Text style={styles.connectionToggleText}>{connectionOpen ? "Hide" : "Setup"}</Text>
            </Pressable>
          </View>
          {connectionOpen ? sharedForm : null}
        </View>

        {message ? (
          <View style={styles.messageBox} accessibilityLiveRegion="polite" accessibilityRole="alert">
            <Text style={styles.messageText}>{message}</Text>
          </View>
        ) : null}
        {busy ? <ActivityIndicator accessibilityLabel={paymentActive ? 'Secure payment in progress' : 'Contacting gateway'} color="#e5653d" style={styles.loader} /> : null}

        {role === "Customer" ? (
          <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>YOUR ORDER · THIS SESSION</Text>
              <Text style={styles.sectionTitle}>{checkout ? `Basket ${checkout.status.replaceAll('_', ' ')}` : orderCount ? `${orderCount} order(s) ready for checkout` : cart.count ? `${cart.count} item(s) · ${money(cart.cents)}` : 'Start with a local store'}</Text>
              <Text style={styles.helpText}>{checkout ? 'Last server response, not live tracking. Payment confirmation requires a webhook.' : 'Cart prices are merchandise only. Review fees and delivery in the server-created basket.'}</Text>
              <View style={styles.rolePicker}>
                {customerStages.map(item => <Pressable key={item} accessibilityRole="tab" accessibilityLabel={`${item} order step`} accessibilityState={{ selected: stage === item }} onPress={() => setStage(item)} style={[styles.roleButton, stage === item && styles.roleButtonActive]}><Text style={[styles.roleText, stage === item && styles.roleTextActive]}>{item}</Text></Pressable>)}
              </View>
            </View>
            {stage === 'Browse' ? <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>01 / DISCOVER</Text>
              <Text style={styles.sectionTitle}>Good things, close by.</Text>
              <Text style={styles.helpText}>
                Find subscription-active grocery and retail partners within 500 meters of your
                restaurant.
              </Text>
              <Field
                editable={!orderLocked && !orderIds.trim()}
                label="Anchor restaurant store ID"
                onChangeText={(value) => {
                  setAnchorStore(value);
                  setCatalogStoreID(value);
                  setCatalog([]);
                  setCatalogLoaded(false);
                  setQuantities({});
                  setNearby([]);
                  setNearbyLoaded(false);
                }}
                value={anchorStore}
              />
              <View style={styles.actionRow}>
                <ActionButton disabled={orderLocked} label="Find nearby shops" onPress={() => void findNearby()} />
                <ActionButton
                  disabled={orderLocked}
                  label="Browse anchor catalog"
                  onPress={() => void loadCatalog(anchorStore)}
                  secondary
                />
              </View>
              {!nearby.length ? <StateCard title={nearbyLoaded ? 'No nearby partners returned' : 'Find a store to begin'}>{nearbyLoaded ? 'No eligible shops were returned within 500 meters. You can still browse the anchor store catalog.' : 'Enter the restaurant location UUID supplied for this beta. Address search is not available yet. No location has been selected for you.'}</StateCard> : null}
              {nearby.map((merchant) => (
                <View key={merchant.merchantId}>
                  <View style={styles.resultRow}>
                    <View style={styles.resultIcon}><Text style={styles.resultIconText}>+</Text></View>
                    <View style={styles.resultCopy}>
                      <Text style={styles.resultTitle}>{merchant.name}</Text>
                      <Text style={styles.resultSub}>{merchant.merchantType} · {Math.round(merchant.distanceMeters)} m away</Text>
                    </View>
                  </View>
                  <ActionButton
                    disabled={orderLocked}
                    label={`Browse ${merchant.name}`}
                    onPress={() => void loadCatalog(merchant.storeLocationId)}
                    secondary
                  />
                </View>
              ))}
            </View>
            </> : null}
            {stage === 'Cart' ? <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>02 / BUILD YOUR ORDER</Text>
              <Text style={styles.sectionTitle}>Choose items from the block.</Text>
              <Field
                editable={!orderLocked}
                label="Store location ID"
                onChangeText={(value) => {
                  setCatalogStoreID(value);
                  setCatalogLoaded(false);
                  setCatalog([]);
                  setQuantities({});
                }}
                value={catalogStoreID}
              />
              <ActionButton disabled={orderLocked} label="Load store catalog" onPress={() => void loadCatalog()} />
              {!catalog.length ? <StateCard title={catalogLoaded ? 'No available items' : 'Your cart starts here'}>{catalogLoaded ? 'This store returned an empty catalog. Choose another store from Browse.' : 'Choose a store in Browse, or enter its location UUID here and load the catalog.'}</StateCard> : null}
              {catalog.map((item) => {
                const quantity = quantities[item.id] ?? 0;
                return (
                  <View key={item.id} style={styles.catalogRow}>
                    <View style={styles.resultCopy}>
                      <Text style={styles.resultTitle}>{item.name}</Text>
                      {item.description ? <Text style={styles.resultSub}>{item.description}</Text> : null}
                      <Text style={styles.resultSub}>
                        {money(item.unitPriceCents)}
                      </Text>
                    </View>
                    <View style={styles.quantityControl}>
                      <Pressable
                        accessibilityLabel={`Decrease ${item.name} quantity`}
                        accessibilityRole="button"
                        disabled={orderLocked || quantity === 0}
                        accessibilityState={{ disabled: orderLocked || quantity === 0 }}
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
                        disabled={orderLocked || quantity >= 99}
                        accessibilityState={{ disabled: orderLocked || quantity >= 99 }}
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
              <StateCard title={`${cart.count} item(s) · ${money(cart.cents)}`}>Merchandise subtotal for this unsent cart only. Created orders are separate and will be included in checkout.</StateCard>
              <Text style={styles.helpText}>Delivery coordinates use decimal degrees (longitude may be negative). Delivery window: {readableDate(deliveryWindow.start)} – {readableDate(deliveryWindow.end)}. This is a requested window, not a guaranteed arrival.</Text>
              <View style={styles.fieldGrid}>
                <Field editable={!orderLocked && !orderIds.trim()} label="Drop-off latitude (-90 to 90)" onChangeText={setLatitude} value={latitude} />
                <Field editable={!orderLocked && !orderIds.trim()} label="Drop-off longitude (-180 to 180)" onChangeText={setLongitude} value={longitude} />
              </View>
              <Field editable={!orderLocked && !orderIds.trim()} label="Delivery start (ISO timestamp with timezone)" value={deliveryWindow.start} onChangeText={start => setDeliveryWindow(current => ({ ...current, start }))} />
              <Field editable={!orderLocked && !orderIds.trim()} label="Delivery end (ISO timestamp with timezone)" value={deliveryWindow.end} onChangeText={end => setDeliveryWindow(current => ({ ...current, end }))} />
              <ActionButton disabled={orderLocked || Boolean(orderIds.trim())} label="Use a new window starting in 15 minutes" onPress={() => setDeliveryWindow(defaultDeliveryWindow())} secondary />
              {orderAttempt.current ? <StateCard title="Order request awaiting reconciliation">Details are locked. Retry unchanged with the same request key. If the result remains uncertain, ask support before starting another order.</StateCard> : null}
              <ActionButton
                disabled={busy || Boolean(checkout) || Boolean(checkoutAttempt.current) || cart.count === 0}
                label={orderAttempt.current ? 'Retry unchanged order' : 'Save order and continue to checkout'}
                onPress={() => void createCustomerOrder()}
              />
            </View>
            </> : null}
            {stage === 'Checkout' ? <>
            <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>03 / CHECKOUT</Text>
              <Text style={styles.sectionTitle}>Bring the whole block together.</Text>
              <Text style={styles.helpText}>
                Create one order from each store catalog you want to include, then create one unified checkout.
              </Text>
              <Text style={styles.helpText}>{orderCount ? `${orderCount} order(s) included. Use Browse to add another store before creating the basket.` : 'No orders yet. Build a cart and save an order first.'}</Text>
              {savedOrders.map(order => <View key={order.id} style={styles.merchantItem}>
                <Text style={styles.resultTitle}>{order.itemCount} item(s) · {money(order.itemSubtotalCents)}</Text>
                <Text style={styles.resultSub}>Order · {order.id}</Text>
                <Text style={styles.resultSub}>Store · {order.storeLocationId}</Text>
                <Text style={styles.resultSub}>Last returned order state · {order.status.replaceAll('_', ' ')}</Text>
              </View>)}
              {orderCount ? <Text style={styles.helpText}>Requested delivery: {readableDate(deliveryWindow.start)} – {readableDate(deliveryWindow.end)}. Drop-off: {latitude}, {longitude}. Saved orders cannot be edited or cancelled here.</Text> : null}
              <ActionButton disabled={orderLocked} label="Add items from another store" onPress={() => setStage('Browse')} secondary />
              <ActionButton disabled={busy} label={advancedOrders ? 'Hide advanced order IDs' : 'Show advanced order IDs'} onPress={() => setAdvancedOrders(value => !value)} secondary />
              {advancedOrders ? <Field
                editable={!orderLocked}
                label="Order IDs (comma separated)"
                onChangeText={(value) => {
                  setOrderIds(value);
                }}
                value={orderIds}
              /> : null}
              <ActionButton
                disabled={busy || Boolean(checkout) || Boolean(orderAttempt.current) || !orderCount}
                label={checkout ? 'Basket created' : checkoutAttempt.current ? 'Retry unchanged basket' : 'Create basket'}
                onPress={() => void createCheckout()}
              />
              {!checkout ? (
                <View>
                  <ActionButton
                    disabled={busy || Boolean(checkoutAttempt.current)}
                    label="Refresh priority test pricing"
                    onPress={() => setPricingRefresh((value) => value + 1)}
                  />
                  {priorityPrice ? (
                    <>
                      <Text style={styles.helpText}>
                        TEST ONLY · Priority shopping {money(priorityPrice.shoppingCents)} + priority delivery {money(priorityPrice.deliveryCents)} = {money(priorityPrice.totalCents)} extra.
                      </Text>
                      <ActionButton
                        disabled={busy || Boolean(checkoutAttempt.current)}
                        label={prioritySelected ? "Priority service selected — use standard" : "Choose priority service (test only)"}
                        onPress={() => setPrioritySelected((selected) => !selected)}
                      />
                      {!priorityPrice.publicCheckoutAvailable ? <Text style={styles.tinyNote}>Public basket creation and payment remain blocked by the gateway. Pricing review only.</Text> : null}
                    </>
                  ) : <Text style={styles.tinyNote}>Priority service unavailable. No surcharge selected.</Text>}
                  <Text style={styles.tinyNote}>Priority service is not a faster-driving or arrival-time guarantee.</Text>
                  {checkoutAttempt.current ? <Text style={styles.tinyNote}>Retry keeps the original basket and priority choice; displayed pricing is not a reserved quote.</Text> : null}
                </View>
              ) : null}
              {checkout ? (
                <View style={styles.checkoutBox}>
                  <Text style={styles.checkoutLabel}>BASKET {checkout.id}</Text>
                  {checkout.priority?.testOnly ? <Text style={styles.helpText}>TEST ONLY · Priority service. No arrival-time guarantee.</Text> : null}
                  {checkout.invoiceLines.map((line, index) => (
                    <View key={`${line.type}-${index}`} style={styles.invoiceRow}>
                      <Text style={styles.invoiceLabel}>{line.type.replaceAll("_", " ")}</Text>
                      <Text style={styles.invoiceAmount}>
                        {money(line.amountCents, checkout.currency)}
                      </Text>
                    </View>
                  ))}
                  <View style={[styles.invoiceRow, styles.totalRow]}>
                    <Text style={styles.totalLabel}>Total</Text>
                    <Text style={styles.totalValue}>
                      {money(checkout.totalCents, checkout.currency)}
                    </Text>
                  </View>
                  <ActionButton
                    disabled={busy || !stripeKeyConfigured || paymentState !== 'not_started'}
                    label={paymentState === 'submitted' ? 'Submitted · awaiting confirmation' : paymentState === 'uncertain' ? 'Reconcile payment before retrying' : stripeKeyConfigured ? 'Continue to secure payment' : 'Add Stripe publishable key to pay'}
                    onPress={() => void payCheckout()}
                  />
                  <Text style={styles.tinyNote}>Checkout ID · {checkout.id}</Text>
                  <ActionButton label="View basket status" onPress={() => setStage('Status')} secondary />
                </View>
              ) : null}
            </View>
            </> : null}
            {stage === 'Status' ? <View style={styles.card}>
              <Text style={styles.sectionEyebrow}>BASKET STATUS / LAST RESPONSE</Text>
              <Text style={styles.sectionTitle}>{checkout ? checkout.status.replaceAll('_', ' ') : 'No basket created yet'}</Text>
              <Text style={styles.helpText}>{checkout ? `${money(checkout.totalCents, checkout.currency)} · ${checkout.id}` : 'Saved orders appear in Checkout. Create a basket there to see its returned status.'}</Text>
              {checkout ? <StateCard title={paymentState === 'submitted' ? 'Payment submitted · confirmation pending' : paymentState === 'uncertain' ? 'Payment outcome uncertain · contact support' : 'Payment not confirmed in this session'}>{paymentState === 'uncertain' ? 'Do not submit another payment or create a replacement basket until support reconciles this basket ID. Closing a sheet or losing connectivity does not prove a payment failed.' : 'The payment sheet and last basket response are not a live payment-status check. Keep this basket ID; only the backend and Stripe reconciliation can confirm the outcome.'}</StateCard> : null}
              <StateCard title="No live tracking or order history here">This app does not fetch customer delivery progress or past orders. Payment-sheet submission is not confirmation of payment or delivery. Keep your basket ID for support and reconcile uncertain payments before starting again.</StateCard>
              <ActionButton label="Return to checkout" onPress={() => setStage('Checkout')} secondary />
              {paymentState === 'uncertain' ? <ActionButton disabled={busy} label="Support confirmed this basket is safe to retry" onPress={() => Alert.alert('Retry only after reconciliation', 'Continue only if support verified that this same basket can safely retry its existing payment. This does not check Stripe or cancel a payment.', [
                { text: 'Keep blocked', style: 'cancel' },
                { text: 'Confirmed safe by support', onPress: () => { setPaymentState('not_started'); setStage('Checkout'); } },
              ])} secondary /> : null}
              <ActionButton disabled={busy || Boolean(orderAttempt.current) || Boolean(checkoutAttempt.current)} label="Start another basket · clear local records" onPress={startNewBasket} secondary />
            </View> : null}
          </>
        ) : null}

        {role === "Driver" ? (
          <View style={styles.card}>
            <Text style={styles.sectionEyebrow}>DRIVER / ACTIVE DELIVERY</Text>
            <Text style={styles.sectionTitle}>Every stop, in the right order.</Text>
            <Text style={styles.helpText}>
              Load your assigned route and share location every five seconds while this screen is
              open. Stop sequencing is controlled by the server.
            </Text>
            <Text style={styles.tinyNote}>Use the route UUID provided by dispatch. Route discovery is not available here; a 403 can mean dispatch is disabled. No simulated deliveries.</Text>
            <Field editable={!tracking && !trackingStarting && !busy && !stopAttempt.current} label="Assigned route UUID from dispatch" onChangeText={value => { endTracking(); setRouteId(value); setRoute(null); }} value={routeId} />
            <ActionButton disabled={busy} label="Load route" onPress={() => void loadRoute()} />
            {!route ? <StateCard title="Waiting for your assigned route">Ask dispatch for a route UUID. Loading requires an authorized driver session and enabled dispatch. A blocked request does not mean you have no assignments. No map or route is simulated.</StateCard> : null}
            {route ? (
              <View style={styles.routeBox}>
                <Text style={styles.checkoutLabel}>ROUTE · {route.status.toUpperCase()}</Text>
                <Text style={styles.helpText}>{route.stops.filter(stop => stop.status === 'completed').length} of {route.stops.length} stops completed. {activeStop ? `Next: ${activeStop.type === 'dropoff' ? 'customer handoff' : 'store pickup'}, stop ${activeStop.sequence}.` : 'No remaining stops returned.'}</Text>
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
                {activeStop && routeCanProgress(route) ? (
                  <View style={styles.stopActions}>
                    <ActionButton disabled={busy} label="Open directions to next stop" onPress={() => void openDirections()} secondary />
                    <Text style={styles.tinyNote}>Opens external navigation with the next stop coordinates. Returning to ShopStops does not resume GPS automatically after backgrounding.</Text>
                    <Text style={styles.helpText}>Confirm arrival only at the location. Confirm pickup after receiving the items; confirm delivery only after the actual customer handoff.</Text>
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
                  <Text style={styles.tinyNote}>{activeStop ? 'This route is not actionable. Refresh it or contact dispatch; no stop progress is inferred.' : 'All route stops are complete.'}</Text>
                )}
                {stopAttempt.current ? <StateCard title="Stop request awaiting reconciliation">Retry the unchanged stop action, or refresh this route to check the server state. Switching routes is locked until the event is reconciled.</StateCard> : null}
                <Text style={styles.tinyNote}>Routing matrix · {route.routingProvider}</Text>
              </View>
            ) : null}
            <ActionButton
              disabled={busy || tracking || trackingStarting || !routeCanProgress(route)}
              label={trackingStarting ? "Requesting location access…" : tracking ? "Location sharing is active" : "Start 5-second location sharing"}
              onPress={() => void beginTracking()}
            />
            <Text style={styles.tinyNote}>Location permission is requested only when you start sharing. Updates stop when the app backgrounds, you change roles, or you clear the session. No background tracking.</Text>
            <StateCard title={tracking ? 'GPS sharing active on this device' : 'GPS sharing off'}>{`${lastLocationReport ? `Last position accepted by gateway: ${readableDate(lastLocationReport)}.` : 'No position acknowledged by the gateway in this sharing session.'}${locationError ? ` Latest reporting error: ${locationError}` : ''}`}</StateCard>
            {tracking ? <ActionButton label="End location sharing" onPress={endTracking} secondary /> : null}
          </View>
        ) : null}

        {role === "Merchant" ? (
          <>
            <View style={styles.card}>
              <StateCard title="Store setup · location → catalog → billing">Load your own locations first, then add or update items. Check billing below: a catalog alone does not activate your storefront. If no locations are returned, contact your operator for store provisioning.</StateCard>
              <Text style={styles.sectionEyebrow}>MERCHANT / CATALOG</Text>
              <Text style={styles.sectionTitle}>Manage your store items.</Text>
              <Text style={styles.helpText}>
                Set prices and delivery volume. Items you mark unavailable disappear from customer catalogs; previous order prices remain unchanged.
              </Text>
              <ActionButton
                disabled={busy || Boolean(merchantCatalogAttempt.current)}
                label="Load my store locations"
                onPress={() => void loadMerchantStoreLocations()}
                secondary
              />
              {merchantStoreLocations.map((location) => (
                <Pressable
                  accessibilityRole="radio"
                  disabled={busy || Boolean(merchantCatalogAttempt.current)}
                  accessibilityState={{ selected: merchantStoreLocation === location.id, disabled: busy || Boolean(merchantCatalogAttempt.current) }}
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
                <Text style={styles.tinyNote}>{locationsLoaded ? 'No store locations returned for this account. Contact your operator.' : 'Load store locations before creating catalog items.'}</Text>
              ) : null}
              <Field editable={!busy && !merchantCatalogAttempt.current} label="Item name" onChangeText={setNewItemName} value={newItemName} />
              <Field editable={!busy && !merchantCatalogAttempt.current} label="Description" onChangeText={setNewItemDescription} value={newItemDescription} />
              <View style={styles.fieldGrid}>
                <Field editable={!busy && !merchantCatalogAttempt.current} label="Price (cents)" keyboardType="decimal-pad" onChangeText={setNewItemPrice} value={newItemPrice} />
                <Field editable={!busy && !merchantCatalogAttempt.current} label="Volume (liters)" keyboardType="decimal-pad" onChangeText={setNewItemVolume} value={newItemVolume} />
              </View>
              <Text style={styles.tinyNote}>Price: 1–10000000 whole cents. Volume: up to 100000 liters, at most two decimal places. Item name: 200 UTF-8 bytes; description: 1000.</Text>
              {merchantCatalogAttempt.current ? <StateCard title="Catalog creation awaiting reconciliation">Retry unchanged with the same request key. Refresh the catalog to inspect results; contact support if the original result remains uncertain before creating a replacement item.</StateCard> : null}
              <ActionButton
                disabled={busy || !merchantStoreLocation}
                label={merchantCatalogAttempt.current ? 'Retry unchanged catalog item' : 'Add catalog item'}
                onPress={() => void createMerchantCatalogItem()}
              />
              <ActionButton
                disabled={busy}
                label="Refresh my catalog"
                onPress={() => void loadMerchantCatalog()}
                secondary
              />
              {merchantCatalog.length === 0 ? (
                <Text style={styles.tinyNote}>{merchantCatalogLoaded ? 'Your catalog is empty. Choose a store and add its first item above.' : 'Catalog not loaded. Refresh to request current items for your merchant account.'}</Text>
              ) : null}
              {merchantCatalog.map((item) => {
                const draft = merchantCatalogDrafts[item.id];
                if (!draft) return null;
                return (
                  <View key={item.id} style={styles.merchantItem}>
                    <Text style={styles.resultSub}>Store · {item.storeLocationId}</Text>
                    <Field
                      editable={!busy}
                      label="Item name"
                      onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                        ...current, [item.id]: { ...current[item.id], name: value },
                      }))}
                      value={draft.name}
                    />
                    <Field
                      editable={!busy}
                      label="Description"
                      onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                        ...current, [item.id]: { ...current[item.id], description: value },
                      }))}
                      value={draft.description}
                    />
                    <View style={styles.fieldGrid}>
                      <Field
                        editable={!busy}
                        label="Price (cents)"
                        keyboardType="decimal-pad"
                        onChangeText={(value) => setMerchantCatalogDrafts((current) => ({
                          ...current, [item.id]: { ...current[item.id], unitPriceCents: value },
                        }))}
                        value={draft.unitPriceCents}
                      />
                      <Field
                        editable={!busy}
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
                Refresh to review the server-reported subscription amount and visibility. Saving a payment method does not activate the storefront; a successful invoice is required.
              </Text>
              <View style={styles.actionRow}>
                <ActionButton disabled={busy} label="Refresh billing status" onPress={() => void refreshSubscription()} />
                <ActionButton disabled={busy || !stripeKeyConfigured} label="Save payment method" onPress={() => void setupMerchantPaymentMethod()} secondary />
              </View>
              {subscription ? (
                <View style={styles.statusCard}>
                  <Text style={styles.statusWord}>{subscription.status.replaceAll("_", " ")}</Text>
                  <Text style={styles.resultTitle}>{money(subscription.monthlyAmountCents, subscription.currency)} / month</Text>
                  <Text style={styles.resultSub}>
                    {subscription.storefrontVisible ? "Storefront visible" : "Storefront hidden"}
                    {" · "}
                    {subscription.paymentMethodReady ? "Payment method saved" : "Payment method needed"}
                  </Text>
                  {subscription.currentPeriodEnd ? (
                    <Text style={styles.resultSub}>Current period ends {readableDate(subscription.currentPeriodEnd)}</Text>
                  ) : null}
                </View>
              ) : <StateCard title="Billing not loaded">Refresh billing status to see your subscription amount, saved-payment-method readiness, and storefront visibility.</StateCard>}
              <ActionButton
                disabled={busy || !subscription?.paymentMethodReady || subscription.status === 'active'}
                label={subscription ? `Start subscription · ${money(subscription.monthlyAmountCents, subscription.currency)} / month` : 'Load billing status before subscribing'}
                onPress={() => void startSubscription()}
              />
              <Text style={styles.tinyNote}>Billed monthly through Stripe. Server-reported billing status is authoritative; visibility is not assumed.</Text>
            </View>
          </>
        ) : null}

        <Text style={styles.footer}>
          Requests require your identity gateway · ShopStops {new Date().getFullYear()}
        </Text>
      </ScrollView>
    </KeyboardAvoidingView>
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
  sectionEyebrow: { color: "#a84020", fontSize: 10, fontWeight: "800", letterSpacing: 1.4 },
  sectionTitle: { color: "#19352f", fontSize: 20, fontWeight: "800", marginTop: 7 },
  helpText: { color: "#65756f", fontSize: 13, lineHeight: 19, marginTop: 6, marginBottom: 11 },
  field: { flex: 1, minWidth: 125, marginTop: 6 },
  fieldLabel: { color: "#52645e", fontSize: 11, fontWeight: "700", marginBottom: 6 },
  fieldGrid: { flexDirection: "row", flexWrap: "wrap", gap: 10 },
  input: { backgroundColor: "#f8f9f7", borderColor: "#e4e9e5", borderWidth: 1, borderRadius: 10, color: "#203b34", fontSize: 13, paddingHorizontal: 12, paddingVertical: 11, marginTop: 8 },
  actionButton: { backgroundColor: "#ad4020", minHeight: 48, justifyContent: "center", alignItems: "center", borderRadius: 11, paddingHorizontal: 15, marginTop: 13 },
  actionButtonSecondary: { backgroundColor: "#e9f0ec" },
  actionButtonDisabled: { opacity: 0.48 },
  actionText: { color: "#fff", fontWeight: "800", fontSize: 12, textAlign: "center" },
  actionTextSecondary: { color: "#24443b" },
  actionRow: { gap: 3 },
  messageBox: { padding: 13, borderRadius: 11, backgroundColor: "#fff1e9", marginTop: 12 },
  messageText: { color: "#8e4229", fontSize: 12, lineHeight: 18 },
  loader: { marginTop: 12 },
  resultRow: { flexDirection: "row", alignItems: "center", paddingVertical: 12, borderTopWidth: 1, borderTopColor: "#edf0ed", marginTop: 12 },
  resultIcon: { width: 38, height: 38, borderRadius: 12, backgroundColor: "#e7f1eb", justifyContent: "center", alignItems: "center", marginRight: 11 },
  resultIconText: { color: "#397457", fontSize: 22, fontWeight: "500" },
  resultCopy: { flex: 1 },
  resultTitle: { color: "#213b34", fontWeight: "800", fontSize: 13 },
  resultSub: { color: "#52645b", fontSize: 12, marginTop: 4 },
  catalogRow: { flexDirection: "row", alignItems: "center", paddingVertical: 12, borderTopWidth: 1, borderTopColor: "#edf0ed", marginTop: 10 },
  merchantItem: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 14 },
  locationChoice: { borderWidth: 1, borderColor: "#e4e9e5", borderRadius: 10, padding: 11, marginTop: 8 },
  locationChoiceActive: { borderColor: "#397457", backgroundColor: "#e7f1eb" },
  quantityControl: { flexDirection: "row", alignItems: "center", gap: 10, marginLeft: 10 },
  quantityButton: { width: 44, height: 44, borderRadius: 22, backgroundColor: "#e9f0ec", alignItems: "center", justifyContent: "center" },
  quantityButtonText: { color: "#24443b", fontSize: 17, fontWeight: "800" },
  quantityText: { minWidth: 16, color: "#213b34", textAlign: "center", fontWeight: "800" },
  checkoutBox: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 16 },
  routeBox: { backgroundColor: "#f8f9f7", borderRadius: 13, padding: 14, marginTop: 14 },
  stopActions: { marginTop: 3 },
  checkoutLabel: { color: "#77857f", fontSize: 9, fontWeight: "800", letterSpacing: 1.2, marginBottom: 11 },
  invoiceRow: { flexDirection: "row", justifyContent: "space-between", paddingVertical: 7 },
  invoiceLabel: { color: "#5f706a", fontSize: 12, textTransform: "capitalize" },
  invoiceAmount: { color: "#213b34", fontSize: 12, fontWeight: "700" },
  totalRow: { borderTopWidth: 1, borderTopColor: "#dfe6e1", marginTop: 4, paddingTop: 11 },
  totalLabel: { color: "#203b34", fontWeight: "800", fontSize: 13 },
  totalValue: { color: "#203b34", fontWeight: "900", fontSize: 15 },
  tinyNote: { color: "#52645b", fontSize: 12, marginTop: 9, lineHeight: 18 },
  stopRow: { flexDirection: "row", alignItems: "center", paddingVertical: 9 },
  stopNumber: { width: 28, height: 28, borderRadius: 14, backgroundColor: "#e5653d", justifyContent: "center", alignItems: "center", marginRight: 11 },
  stopNumberLast: { backgroundColor: "#397457" },
  stopNumberText: { color: "#fff", fontWeight: "800", fontSize: 11 },
  statusCard: { backgroundColor: "#f8f9f7", padding: 15, borderRadius: 12, marginTop: 15 },
  statusWord: { color: "#24443b", fontWeight: "900", fontSize: 18, textTransform: "capitalize", marginBottom: 5 },
  footer: { color: "#52645b", textAlign: "center", fontSize: 12, marginTop: 25, lineHeight: 18 },
});
