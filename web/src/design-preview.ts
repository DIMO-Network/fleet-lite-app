// TEMPORARY design-review entry for design-preview.html — delete with it.
// Mounts the trips + behaviour panels without auth: telemetry calls are
// stubbed so the panels fall through to their BEHAVIOR_DEMO mock data.
import './global-styles.ts';
import './elements/vehicle-trips-panel.ts';
import './elements/vehicle-behavior-panel.ts';
import { TelemetryService } from './services/telemetry-service.ts';
import { themeService } from './services/theme-service.ts';

const svc = TelemetryService.getInstance() as unknown as Record<string, unknown>;
svc.fleetLocations = () => Promise.resolve({ locations: { '186612': { lat: 25.7617, lon: -80.1918 } }, noPermissions: [] });
svc.segments = () => Promise.reject(new Error('design preview: no API'));
svc.tripRoute = () => Promise.resolve({ points: [] });
svc.tripGeofences = () => Promise.resolve({ geofences: [] });
svc.tripReplay = () => Promise.resolve({ waypoints: [], events: [] });

document.getElementById('theme')?.addEventListener('click', () => themeService.toggle());
