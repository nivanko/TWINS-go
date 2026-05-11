import React from 'react';
import { useTranslation } from 'react-i18next';
import { UseFormRegister, FieldError } from 'react-hook-form';
import { Clipboard, BookOpen, UserPlus, X } from 'lucide-react';
import { useAddressValidation, getValidationStatus } from '@/shared/hooks/useAddressValidation';
import { IconButton } from '@/shared/components/IconButton';

// Quick client-side address format check (same regex as Send.tsx / AddressBookDialog.tsx)
const TWINS_ADDRESS_REGEX = /^[Wamn][a-km-zA-HJ-NP-Z1-9]{33}$/;

// Design tokens — Receive page reference (see frontend/CLAUDE.md "Design Tokens")
const inputStyle: React.CSSProperties = {
  flex: 1,
  padding: '7px 10px',
  fontSize: '12px',
  backgroundColor: '#252525',
  border: '1px solid #3a3a3a',
  borderRadius: '4px',
  color: '#ddd',
  outline: 'none',
};
const rowCardStyle: React.CSSProperties = {
  backgroundColor: '#2a2a2a',
  border: '1px solid #3a3a3a',
  borderRadius: '6px',
  padding: '12px',
  display: 'flex',
  flexDirection: 'row',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: '8px',
  transition: 'border-color 0.15s',
};
const errorMessageStyle: React.CSSProperties = {
  marginLeft: '0',
  marginTop: '-4px',
  fontSize: '11px',
  flexBasis: '100%',
};

interface RecipientFieldErrors {
  address?: FieldError;
  amount?: FieldError;
  label?: FieldError;
}

interface RecipientFieldProps {
  index: number;
  register: UseFormRegister<any>;
  address: string;
  label: string;
  showRemoveButton: boolean;
  onRemove: () => void;
  onUseMaximum?: () => void;
  onAddressBookPick?: () => void;
  onSaveToAddressBook?: (address: string, label: string) => void;
  errors?: RecipientFieldErrors;
}

const RecipientFieldComponent: React.FC<RecipientFieldProps> = ({
  index,
  register,
  address,
  label,
  showRemoveButton,
  onRemove,
  onUseMaximum,
  onAddressBookPick,
  onSaveToAddressBook,
  errors
}) => {
  const { t } = useTranslation('wallet');
  // Use address validation hook for this specific recipient
  const addressValidation = useAddressValidation(address || '');
  const validationStatus = getValidationStatus(addressValidation);

  // Generate IDs for accessibility
  const addressInputId = `recipient-address-${index}`;
  const addressErrorId = `recipient-address-error-${index}`;
  const labelInputId = `recipient-label-${index}`;
  const amountInputId = `recipient-amount-${index}`;

  // Address border color reflects form-error / async validation state
  const addressBorderColor =
    errors?.address || validationStatus.status === 'invalid' || validationStatus.status === 'error'
      ? '#ff6666'
      : validationStatus.status === 'valid'
      ? '#27ae60'
      : validationStatus.status === 'warning'
      ? '#ff9966'
      : '#3a3a3a';

  return (
    <div
      style={rowCardStyle}
      onMouseEnter={(e) => {
        e.currentTarget.style.borderColor = '#444';
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.borderColor = '#3a3a3a';
      }}
    >
      {/* Header: only when multiple recipients */}
      {showRemoveButton && (
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexBasis: '100%' }}>
          <span style={{ fontSize: '11px', color: '#888' }}>
            {t('send.recipients.title', { number: index + 1 })}
          </span>
          <IconButton
            icon={<X size={12} />}
            title={t('send.recipients.remove')}
            ariaLabel={t('send.recipients.remove')}
            onClick={onRemove}
            variant="danger"
          />
        </div>
      )}

      {/* Pay To row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '6px', flex: '1 1 480px', minWidth: 0 }}>
        <div style={{ flex: 1, position: 'relative' }}>
          <input
            {...register(`recipients.${index}.address`)}
            id={addressInputId}
            type="text"
            placeholder={t('send.payToPlaceholder')}
            aria-label={t('send.payTo')}
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={!!errors?.address || validationStatus.status === 'invalid' || validationStatus.status === 'error'}
            aria-describedby={errors?.address?.message || validationStatus.message ? addressErrorId : undefined}
            style={{
              ...inputStyle,
              width: '100%',
              flex: undefined,
              paddingRight: validationStatus.status !== 'idle' ? '28px' : '10px',
              borderColor: addressBorderColor,
            }}
          />
          {validationStatus.status !== 'idle' && (
            <span
              style={{
                position: 'absolute',
                right: '8px',
                top: '50%',
                transform: 'translateY(-50%)',
                fontSize: '12px',
                pointerEvents: 'none',
              }}
            >
              {validationStatus.status === 'validating' && '⏳'}
              {validationStatus.status === 'valid' && '✓'}
              {validationStatus.status === 'invalid' && '✗'}
              {validationStatus.status === 'warning' && '⚠️'}
              {validationStatus.status === 'error' && '❌'}
            </span>
          )}
        </div>
        {/* Clipboard paste — currently dead UI (no onClick wired in upstream); preserved as
            visual placeholder. Wiring is tracked as a separate follow-up; see task User Notes. */}
        <IconButton
          icon={<Clipboard size={12} />}
          title={t('common:buttons.paste')}
          ariaLabel={t('common:buttons.paste')}
          onClick={() => {}}
        />
        {onAddressBookPick && (
          <IconButton
            icon={<BookOpen size={12} />}
            title={t('common:buttons.addressBook')}
            ariaLabel={t('common:buttons.addressBook')}
            onClick={onAddressBookPick}
          />
        )}
        {onSaveToAddressBook && (() => {
          // Enable when address passes async validation OR matches format regex
          // (regex fallback avoids delay when address is populated from picker)
          const addressOk = validationStatus.status === 'valid' || TWINS_ADDRESS_REGEX.test(address);
          const canSave = addressOk && !!label.trim();
          return (
            <IconButton
              icon={<UserPlus size={12} />}
              title={t('send.saveToAddressBook')}
              ariaLabel={t('send.saveToAddressBook')}
              onClick={() => onSaveToAddressBook(address, label)}
              disabled={!canSave}
            />
          );
        })()}
      </div>

      {/* Label + Amount row (2-column flex; Send is denser than Receive per research §4.3).
          flexWrap allows columns to stack on narrow widths / long localized labels rather than
          clip — parent SendRecipients sets overflowX: 'hidden'. */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap', flex: '1 1 380px', minWidth: 0 }}>
        {/* Label column */}
        <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: '6px' }}>
          <input
            {...register(`recipients.${index}.label`)}
            id={labelInputId}
            type="text"
            placeholder={t('send.labelPlaceholder')}
            aria-label={t('send.label')}
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            style={inputStyle}
          />
        </div>
        {/* Amount column */}
        <div style={{ flex: 1, display: 'flex', alignItems: 'center', gap: '6px' }}>
          <input
            {...register(`recipients.${index}.amount`)}
            id={amountInputId}
            type="text"
            placeholder={t('send.amountPlaceholder')}
            aria-label={t('send.amount')}
            aria-invalid={!!errors?.amount}
            style={{
              ...inputStyle,
              borderColor: errors?.amount ? '#ff6666' : '#3a3a3a',
            }}
          />
          {onUseMaximum && (
            <button
              type="button"
              onClick={onUseMaximum}
              title={t('send.recipients.useMaximum')}
              style={{
                padding: '4px 8px',
                fontSize: '10px',
                backgroundColor: '#383838',
                border: '1px solid #4a4a4a',
                borderRadius: '4px',
                color: '#ccc',
                cursor: 'pointer',
                whiteSpace: 'nowrap',
                flexShrink: 0,
                transition: 'background-color 0.15s, border-color 0.15s, color 0.15s',
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = '#444';
                e.currentTarget.style.borderColor = '#5a5a5a';
                e.currentTarget.style.color = '#fff';
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = '#383838';
                e.currentTarget.style.borderColor = '#4a4a4a';
                e.currentTarget.style.color = '#ccc';
              }}
            >
              {t('common:buttons.max')}
            </button>
          )}
          <span style={{ fontSize: '11px', color: '#888', minWidth: '45px', flexShrink: 0 }}>
            {t('common:units.twins')}
          </span>
        </div>
      </div>

      {/* Form validation error messages */}
      {errors?.address && (
        <div
          id={addressErrorId}
          role="alert"
          aria-live="polite"
          style={{ ...errorMessageStyle, color: '#ff6666' }}
        >
          {errors.address.message}
        </div>
      )}
      {errors?.amount && (
        <div role="alert" aria-live="polite" style={{ ...errorMessageStyle, color: '#ff6666' }}>
          {errors.amount.message}
        </div>
      )}
      {/* Backend validation messages (only show if no form errors) */}
      {!errors?.address && validationStatus.message && (
        <div
          id={addressErrorId}
          role="alert"
          aria-live="polite"
          style={{
            ...errorMessageStyle,
            color:
              validationStatus.status === 'valid'
                ? '#27ae60'
                : validationStatus.status === 'warning'
                ? '#ff9966'
                : '#ff6666',
          }}
        >
          {validationStatus.message}
        </div>
      )}
    </div>
  );
};

// Export without memoization to ensure react-hook-form registration works properly
export const RecipientField = RecipientFieldComponent;