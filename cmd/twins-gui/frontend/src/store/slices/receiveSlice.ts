import type { SliceCreator } from '../store.types';
import { core } from '@wailsjs/go/models';
import {
  GetReceivingAddresses,
  GetAddressBalances,
  GetPaymentRequests,
  GetCurrentReceivingAddress,
  GenerateReceivingAddress,
  CreatePaymentRequest,
  RemovePaymentRequest,
} from '@wailsjs/go/main/App';

// Re-export types for convenience
export type ReceivingAddress = core.ReceivingAddress;
export type PaymentRequest = core.PaymentRequest;

// Form state for creating payment requests
export interface ReceiveFormState {
  label: string;
  amount: string;
  message: string;
}

export interface ReceiveState {
  // Current receiving address displayed in form
  currentAddress: string;

  // All receiving addresses
  receivingAddresses: ReceivingAddress[];

  // Payment request history
  paymentRequests: PaymentRequest[];

  // When true, reuse the same address for multiple requests (not recommended)
  // When false (default), generate a new address per request
  reuseAddress: boolean;

  // Form field values
  formState: ReceiveFormState;

  // Receiving addresses dialog visibility
  isAddressesDialogOpen: boolean;

  // Request payment dialog visibility
  isRequestDialogOpen: boolean;

  // Currently selected/viewing request
  selectedRequest: PaymentRequest | null;

  // Loading states
  isLoading: boolean;
  isGeneratingAddress: boolean;
  isCreatingRequest: boolean;

  // Per-address spendable balances (address -> balance in TWINS)
  addressBalances: Record<string, number>;

  // Error state
  error: string | null;
}

export interface ReceiveActions {
  // Form state
  setReuseAddress: (reuse: boolean) => void;
  updateFormField: (field: keyof ReceiveFormState, value: string) => void;
  clearForm: () => void;

  // Dialog management
  openAddressesDialog: () => void;
  closeAddressesDialog: () => void;
  openRequestDialog: (request?: PaymentRequest) => void;
  closeRequestDialog: () => void;

  // Picks an existing receiving address as the current address for the
  // Payment Request form on the Receive page. Atomically: sets
  // currentAddress, flips reuseAddress to true (so the picked address
  // survives through createPaymentRequest instead of being replaced by a
  // freshly generated one on the post-submit rotate), closes the
  // addresses dialog, and clears any lingering error. Form fields
  // (label/amount/message) are intentionally preserved so the user does
  // not lose in-progress input.
  selectAddressForRequest: (address: string) => void;

  // Async actions (thunks)
  fetchReceivingAddresses: () => Promise<void>;
  fetchAddressBalances: () => Promise<void>;
  fetchPaymentRequests: () => Promise<void>;
  fetchCurrentAddress: () => Promise<void>;
  generateNewAddress: (label: string) => Promise<ReceivingAddress | null>;
  createPaymentRequest: (unit?: 'TWINS' | 'mTWINS' | 'uTWINS') => Promise<PaymentRequest | null>;
  deletePaymentRequest: (address: string, id: number) => Promise<boolean>;

  // Error handling
  setError: (error: string | null) => void;
  clearError: () => void;

  // Reset
  resetReceiveState: () => void;
}

export type ReceiveSlice = ReceiveState & ReceiveActions;

const initialFormState: ReceiveFormState = {
  label: '',
  amount: '',
  message: '',
};

const initialState: ReceiveState = {
  currentAddress: '',
  receivingAddresses: [],
  paymentRequests: [],
  reuseAddress: false,
  formState: initialFormState,
  isAddressesDialogOpen: false,
  isRequestDialogOpen: false,
  selectedRequest: null,
  addressBalances: {},
  isLoading: false,
  isGeneratingAddress: false,
  isCreatingRequest: false,
  error: null,
};

export const createReceiveSlice: SliceCreator<ReceiveSlice> = (set, get) => ({
  ...initialState,

  // Form state
  setReuseAddress: (reuse) =>
    set(() => ({
      reuseAddress: reuse,
    })),

  updateFormField: (field, value) =>
    set((state) => ({
      formState: {
        ...state.formState,
        [field]: value,
      },
    })),

  clearForm: () =>
    set(() => ({
      formState: { label: '', amount: '', message: '' },
    })),

  // Dialog management
  openAddressesDialog: () =>
    set(() => ({
      isAddressesDialogOpen: true,
    })),

  closeAddressesDialog: () =>
    set(() => ({
      isAddressesDialogOpen: false,
    })),

  openRequestDialog: (request) =>
    set(() => ({
      isRequestDialogOpen: true,
      selectedRequest: request || null,
    })),

  closeRequestDialog: () =>
    set(() => ({
      isRequestDialogOpen: false,
      selectedRequest: null,
    })),

  // See the comment on ReceiveActions.selectAddressForRequest above for
  // the full rationale. Note the reuseAddress = true flip: without it,
  // createPaymentRequest (line ~294) would call generateNewAddress('')
  // after the submit succeeds and replace the user's picked address
  // with a brand-new one, silently discarding the pick.
  selectAddressForRequest: (address) =>
    set(() => ({
      currentAddress: address,
      reuseAddress: true,
      isAddressesDialogOpen: false,
      error: null,
    })),

  // Async actions
  fetchReceivingAddresses: async () => {
    set({ isLoading: true, error: null });
    try {
      const addresses = await GetReceivingAddresses();
      set({ receivingAddresses: addresses, isLoading: false });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to fetch receiving addresses';
      set({ error: message, isLoading: false });
    }
  },

  fetchAddressBalances: async () => {
    try {
      const balances = await GetAddressBalances();
      set({ addressBalances: balances || {} });
    } catch {
      // Silently fail — balances are supplementary info
      set({ addressBalances: {} });
    }
  },

  fetchPaymentRequests: async () => {
    set({ isLoading: true, error: null });
    try {
      const requests = await GetPaymentRequests();
      // Sort by date descending
      const sorted = [...requests].sort(
        (a, b) => new Date(b.date).getTime() - new Date(a.date).getTime()
      );
      set({ paymentRequests: sorted, isLoading: false });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to fetch payment requests';
      set({ error: message, isLoading: false });
    }
  },

  fetchCurrentAddress: async () => {
    set({ isLoading: true, error: null });
    try {
      const address = await GetCurrentReceivingAddress();
      set({ currentAddress: address.address, isLoading: false });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to fetch current address';
      set({ error: message, isLoading: false });
    }
  },

  generateNewAddress: async (label) => {
    // Validate label length
    if (label.length > 100) {
      set({ error: 'Label too long (max 100 characters)', isGeneratingAddress: false });
      return null;
    }

    set({ isGeneratingAddress: true, error: null });
    try {
      const newAddress = await GenerateReceivingAddress(label);
      set((state) => ({
        receivingAddresses: [...state.receivingAddresses, newAddress],
        currentAddress: newAddress.address,
        isGeneratingAddress: false,
      }));
      return newAddress;
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to generate new address';
      set({ error: message, isGeneratingAddress: false });
      return null;
    }
  },

  createPaymentRequest: async (unit = 'TWINS' as 'TWINS' | 'mTWINS' | 'uTWINS') => {
    const state = get();

    // Prevent concurrent requests
    if (state.isCreatingRequest) {
      return null;
    }

    const { currentAddress, formState, reuseAddress } = state;

    // Validate and convert amount based on selected unit
    let amount = 0;
    if (formState.amount) {
      const parsedAmount = parseFloat(formState.amount);
      if (isNaN(parsedAmount) || parsedAmount < 0) {
        set({ error: 'Invalid amount' });
        return null;
      }
      // Convert to TWINS based on unit
      switch (unit) {
        case 'mTWINS':
          amount = parsedAmount / 1000;
          break;
        case 'uTWINS':
          amount = parsedAmount / 1000000;
          break;
        default:
          amount = parsedAmount;
      }
    }

    // Determine which address to use
    let addressToUse = currentAddress;

    // If not reusing address, generate a new one first
    if (!reuseAddress && !currentAddress) {
      const newAddress = await state.generateNewAddress(formState.label);
      if (!newAddress) {
        return null;
      }
      addressToUse = newAddress.address;
    }

    set({ isCreatingRequest: true, error: null });
    try {
      const request = await CreatePaymentRequest(
        addressToUse,
        formState.label,
        formState.message,
        amount
      );

      set((state) => ({
        paymentRequests: [request, ...state.paymentRequests],
        isCreatingRequest: false,
        selectedRequest: request,
        isRequestDialogOpen: true,
      }));

      // Clear form after successful creation
      state.clearForm();

      // If not reusing address, generate a new current address for next time
      if (!reuseAddress) {
        state.generateNewAddress('');
      }

      return request;
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to create payment request';
      set({ error: message, isCreatingRequest: false });
      return null;
    }
  },

  deletePaymentRequest: async (address, id) => {
    set({ error: null });
    try {
      await RemovePaymentRequest(address, id);
      // Filter using both address and id since id is per-address, not global
      set((state) => ({
        paymentRequests: state.paymentRequests.filter(
          (req) => !(req.address === address && req.id === id)
        ),
      }));
      return true;
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to delete payment request';
      set({ error: message });
      return false;
    }
  },

  // Error handling
  setError: (error) =>
    set(() => ({
      error,
    })),

  clearError: () =>
    set(() => ({
      error: null,
    })),

  // Reset
  resetReceiveState: () =>
    set(() => ({
      ...initialState,
    })),
});
